package main

import (
	"bytes"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"text/template"
	"time"
)

func getUsageText() string {
	return fmt.Sprintf(`Usage:
  %s ready -u <service_url> -t <max_retries> [-status <status_code>]
  %s gen -image <image_name> [-port <port>] [-volumes <volume_mapping>]`, os.Args[0], os.Args[0])
}

func main() {
	if len(os.Args) < 2 {
		log.Fatal(getUsageText())
	}

	command := os.Args[1]

	switch command {
	case "ready":
		handleReady()
	case "gen":
		handleGen()
	default:
		log.Fatalf("Unknown command: %s\nAvailable commands: ready, gen", command)
	}
}

func handleReady() {
	readyCmd := flag.NewFlagSet("ready", flag.ExitOnError)
	url := readyCmd.String("u", "", "Service URL to check")
	maxRetries := readyCmd.Int("t", 0, "Maximum number of retries")
	expectedStatus := readyCmd.Int("status", 200, "Expected HTTP status code")

	readyCmd.Parse(os.Args[2:])

	if *url == "" || *maxRetries <= 0 {
		log.Fatalf("Usage: %s ready -u <service_url> -t <max_retries> [-status <status_code>]", os.Args[0])
	}

	// 循环检查服务状态
	for i := 0; i < *maxRetries; i++ {
		// 发送 HTTP 请求检查服务状态
		resp, err := http.Get(*url)
		if err == nil {
			if resp.StatusCode == *expectedStatus {
				log.Printf("Service is up and running with expected status code %d!", *expectedStatus)
				return
			}
			log.Printf("Service returned status code %d, expected %d", resp.StatusCode, *expectedStatus)
		} else {
			log.Printf("Request failed: %v", err)
		}
		log.Printf("Service is not ready (attempt %d/%d), sleep 1s", i+1, *maxRetries)
		time.Sleep(1 * time.Second)
	}

	log.Fatalf("Service could not be started within %d attempts", *maxRetries)
}

func handleGen() {
	genCmd := flag.NewFlagSet("gen", flag.ExitOnError)
	image := genCmd.String("image", "", "Docker image name")
	port := genCmd.String("port", "", "Port mapping")
	volumes := genCmd.String("volumes", "", "Volume mappings (comma separated)")
	envVars := genCmd.String("env", "", "Environment variables (comma separated, e.g., 'KEY1=value1,KEY2=value2')")

	genCmd.Parse(os.Args[2:])

	if *image == "" {
		log.Fatalf("Usage: %s gen -image <image_name> [-port <port>] [-volumes <volume_mappings>] [-env <env_vars>]", os.Args[0])
	}

	// 定义 docker-compose 模板
	composeStr := `services:
  nginx-top:
    image: nginx
    ports:
      - "80:80"
    volumes:
      - ./nginx-config/default-top.conf:/etc/nginx/conf.d/default.conf:ro

  nginx:
    image: nginx
    ports:
      - "81:80"
    volumes:
      - ./nginx-config/default-inner.conf:/etc/nginx/conf.d/default.conf:ro

  srv1:
    image: {{.Image}}{{if .Volumes}}
    volumes:{{range .Volumes}}
      - {{.}}{{end}}{{end}}{{if .Port}}
    ports:
      - "82:{{.Port}}"{{end}}{{if .EnvVars}}
    environment:{{range .EnvVars}}
      - {{.}}{{end}}{{end}}

  srv2:
    image: {{.Image}}{{if .Volumes}}
    volumes:{{range .Volumes}}
      - {{.}}{{end}}{{end}}{{if .EnvVars}}
    environment:{{range .EnvVars}}
      - {{.}}{{end}}{{end}}

  srv3:
    image: {{.Image}}{{if .Volumes}}
    volumes:{{range .Volumes}}
      - {{.}}{{end}}{{end}}{{if .EnvVars}}
    environment:{{range .EnvVars}}
      - {{.}}{{end}}{{end}}
`

	// 定义 nginx 配置模板
	nginxInnerStr := `upstream backend {
    server srv1:{{.Port}};
    server srv2:{{.Port}};
    server srv3:{{.Port}};
}

server {
    listen       80;
    server_name  -;

    location / {
        proxy_set_header        Host $host;
        proxy_set_header        X-Real-IP $remote_addr;
        proxy_set_header        X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header        X-Forwarded-Proto $scheme;
        proxy_pass 				http://backend/;
		proxy_redirect     		off;
    }

    #error_page  404              /404.html;

    # redirect server error pages to the static page /50x.html
    #
    error_page   500 502 503 504  /50x.html;
    location = /50x.html {
        root   /usr/share/nginx/html;
    }
}
`

	nginxTopStr := `upstream backend {
    server nginx:80;
}

server {
    listen       80;
    server_name  -;

    location / {
        proxy_set_header        Host $host;
        proxy_set_header        X-Real-IP $remote_addr;
        proxy_set_header        X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header        X-Forwarded-Proto $scheme;
        proxy_pass 				http://backend/;
		proxy_redirect     		off;
    }

    #error_page  404              /404.html;

    # redirect server error pages to the static page /50x.html
    #
    error_page   500 502 503 504  /50x.html;
    location = /50x.html {
        root   /usr/share/nginx/html;
    }
}
`

	// 解析环境变量
	var envVarsList []string
	if *envVars != "" {
		envVarsList = strings.Split(*envVars, ",")
		for i, env := range envVarsList {
			envVarsList[i] = strings.TrimSpace(env)
		}
	}

	// 解析卷挂载
	var volumesList []string
	if *volumes != "" {
		volumesList = strings.Split(*volumes, ",")
		for i, vol := range volumesList {
			volumesList[i] = strings.TrimSpace(vol)
		}
	}

	// 创建模板数据
	data := struct {
		Image   string
		Port    string
		Volumes []string
		EnvVars []string
	}{
		Image:   *image,
		Port:    *port,
		Volumes: volumesList,
		EnvVars: envVarsList,
	}

	// 如果没有指定端口，使用默认端口 8080
	if data.Port == "" {
		data.Port = "8080"
	}

	// 创建 nginx 配置目录
	if err := os.MkdirAll("nginx-config", 0755); err != nil {
		log.Fatalf("Error creating nginx-config directory: %v", err)
	}

	// 生成 nginx 配置内容并打印
	nginxTmpl := template.Must(template.New("nginx-config").Parse(nginxInnerStr))

	var nginxBuffer bytes.Buffer
	if err := nginxTmpl.Execute(&nginxBuffer, data); err != nil {
		log.Fatalf("Error executing nginx template: %v", err)
	}

	nginxContent := nginxBuffer.String()
	fmt.Println("=== Generated nginx default.conf ===")
	fmt.Println(nginxContent)
	fmt.Println("=== End of nginx default.conf ===")

	// 写入 nginx-inner 配置文件
	if err := os.WriteFile("nginx-config/default-inner.conf", []byte(nginxContent), 0644); err != nil {
		log.Fatalf("Error writing nginx-config/default-inner.conf: %v", err)
	}

	if err := os.WriteFile("nginx-config/default-top.conf", []byte(nginxTopStr), 0644); err != nil {
		log.Fatalf("Error writing nginx-config/default-top.conf: %v", err)
	}

	// 生成 docker-compose 内容并打印
	composeTmpl := template.Must(template.New("docker-compose").Parse(composeStr))

	var composeBuffer bytes.Buffer
	if err := composeTmpl.Execute(&composeBuffer, data); err != nil {
		log.Fatalf("Error executing compose template: %v", err)
	}

	composeContent := composeBuffer.String()
	fmt.Println("=== Generated docker-compose.yml ===")
	fmt.Println(composeContent)
	fmt.Println("=== End of docker-compose.yml ===")

	// 写入 docker-compose.yml 文件
	if err := os.WriteFile("docker-compose.yml", []byte(composeContent), 0644); err != nil {
		log.Fatalf("Error writing docker-compose.yml: %v", err)
	}

	log.Println("Generated docker-compose.yml and nginx-config/default.conf successfully!")
}
