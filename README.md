# GoLangProxy
- simple application written in go lang for proxing http and https with built in self signed certificate function.

### setup project
go mod init proxy

### Running Proxy app without compiling.
go run main.go config.go certificate.go proxy.go utils.go

### Building app:
go build -o proxy
