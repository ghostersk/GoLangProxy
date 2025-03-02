# GoLangProxy
I start making this app, as first GO project because I find it much easier to compile and run then python ( python is still my preffered lang for coding)
The application would be for situation where you do not want to install anything extra and just proxy traffic with one executable, for me it is for windows server what runs application and need to add extra certificate for it.

- simple application written in go lang for proxing http and https with built in self signed certificate function.
- The certificate directory or file name can be specified in config file ( if not exists or provided it creates self sign cert)
- The app also monitoring changes in the `config.yaml` file and updates app after change.
- by default proxy redirects http to https if the url what is proxied is on https
- the redirection can be turned of by setting `true` in `no_https_redirect` with the host name
- By default it trusts any certificate for url what is proxied, this can be disabled in `trust_target`
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
### setup project - powershell:
go mod init golangproxy ; go mod tidy
### setup project - cmd,bash:
go mod init golangproxy && go mod tidy

### Running Proxy app without compiling.
go run main.go

### Building app:
go build -o build/golangproxy.exe
go build -ldflags="-H=windowsgui" -o build/golangproxy.exe

### Known issue.
- currently there is logic to proxy ip address target differently then hostname target.
- this is because if same logic is applied for hostname as for IP target the IP target may experience issue with sessions ( I am not expert, so I do not know what causing it, but some sessions been dicsonnected, part of the website was not acting as normal)
- if logic applied for IP target is applied for hostname Target the website may not load or will be 404