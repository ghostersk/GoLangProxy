# GoLangProxy
- simple application written in go lang for proxing http and https with built in self signed certificate function.
- `config.yaml` default settings in current state would be created as:
```yaml
listen_http: :80                                                                                             
listen_https: :443                                                                                           
cert_dir: ./certificates                                                                                     
cert_file: certificate.pem                                                                                   
key_file: key.pem                                                                                            
routes:
  '*': http://127.0.0.1:80
  main.example.com: http://127.0.0.1:80
trust_target:
  '*': true
  main.example.com: false                                                                                    
no_https_redirect:
  '*': false
  main.example.com: true 
```
### setup project
go mod init proxy

### Running Proxy app without compiling.
go run main.go config.go certificate.go proxy.go utils.go

### Building app:
go build -o proxy
