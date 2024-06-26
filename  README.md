<!-- ビルド -->
GOOS=linux GOARCH=amd64 go build -o bootstrap main.go

zip bootstrap.zip bootstrap

<!-- デプロイ -->
sam deploy