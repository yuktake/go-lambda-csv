package main

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/aws/aws-sdk-go/service/dynamodb/dynamodbattribute"
	"github.com/google/uuid"
)

var (
	tableName = os.Getenv("TABLE_NAME")
	svc       *dynamodb.DynamoDB
)

type Record struct {
	ID string
}

// mainパッケージに書くとmain関数より先に実行されます。
// mainパッケージでない場合は、importするだけで呼び出されます。
func init() {
	sess := session.Must(session.NewSession())
	svc = dynamodb.New(sess)
}

func processRecord(record []string, sem chan struct{}, wg *sync.WaitGroup) {
	// wg.Done()を呼び出すことで、goroutineの数をカウントダウンします
	defer wg.Done()
	// deferを使って、goroutineの処理が終了したらセマフォをリリースする
	defer func() { <-sem }() // セマフォからリリース

	// ここで各レコードの処理を行います
	log.Printf("Processing record: %v", record)

	id := uuid.New().String()
	item := Record{
		ID: id,
	}
	av, err := dynamodbattribute.MarshalMap(item)
	if err != nil {
		fmt.Println("Got error marshalling new item:")
		fmt.Println(err.Error())
		os.Exit(1)
	}

	// Put item into DynamoDB
	input := &dynamodb.PutItemInput{
		TableName: aws.String(tableName),
		Item:      av,
	}

	_, err = svc.PutItem(input)
	if err != nil {
		log.Printf("Error putting item in DynamoDB: %v", err)
		return // エラーが発生した場合は処理を中断
	}
}

func handleRequest(ctx context.Context, request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	if request.HTTPMethod != http.MethodPost {
		return events.APIGatewayProxyResponse{StatusCode: http.StatusMethodNotAllowed}, nil
	}

	// Read CSV data from the request body
	body := request.Body
	log.Printf("Processing body: %v", body)

	contentType := request.Headers["Content-Type"]
	bodyReader := strings.NewReader(body)
	reader := multipart.NewReader(bodyReader, extractBoundary(contentType))

	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Printf("Error reading next part: %v", err)
			return events.APIGatewayProxyResponse{StatusCode: http.StatusInternalServerError, Body: "Error processing file"}, nil
		}

		// Check the form name to identify the file part
		if part.FormName() == "file" {
			csvReader := csv.NewReader(part)
			csvReader.LazyQuotes = true // Allow lazy quotes to handle bare quotes in fields
			lines, err := csvReader.ReadAll()
			if err != nil {
				log.Printf("Error reading CSV: %v", err)
				return events.APIGatewayProxyResponse{StatusCode: http.StatusBadRequest, Body: fmt.Sprintf("Error reading CSV: %v", err)}, nil
			}

			var wg sync.WaitGroup
			// バッファサイズ500のセマフォチャネルを作成する。これにより、同時に500個のゴルーチンが実行可能。
			// これを超えると、新しいゴルーチンはセマフォからトークンを受け取るまでブロックされ、空きができるまで待機します。
			sem := make(chan struct{}, 500)

			for _, line := range lines {
				log.Printf("Processing line: %v", line)
				// wg.Add(1)を呼び出すことで、goroutineの数をカウントアップします
				wg.Add(1)
				// チャネルに空の構造体 struct{}{} を送信することで、セマフォを取得する。
				// Goでは、空の構造体はメモリを消費しないため、セマフォのトークンとしてよく使用されます。
				sem <- struct{}{}
				go processRecord(line, sem, &wg)
			}

			// wg.Done()で全てのgoroutineが終了するまで待機します
			wg.Wait()
		}
	}

	return events.APIGatewayProxyResponse{
		StatusCode: http.StatusOK,
		Body:       "CSV data processed successfully",
	}, nil
}

func extractBoundary(contentType string) string {
	const boundaryPrefix = "boundary="
	for _, param := range strings.Split(contentType, ";") {
		param = strings.TrimSpace(param)
		if strings.HasPrefix(param, boundaryPrefix) {
			return param[len(boundaryPrefix):]
		}
	}
	return ""
}

func main() {
	lambda.Start(handleRequest)
}
