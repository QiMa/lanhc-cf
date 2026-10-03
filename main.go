package main

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"net/smtp"
	"os"
	"strconv"
	"strings"
)

// emailBody is the template used to render the notification email.
const emailBody = `
<h2>网站表单提交</h2>
<p><strong>称呼：</strong>{{.Name}}</p>
<p><strong>邮箱：</strong>{{.Email}}</p>
{{if .Subject}}<p><strong>主题：</strong>{{.Subject}}</p>
{{end}}{{if .Platform}}<p><strong>平台：</strong>{{.Platform}}</p>
{{end}}<p><strong>内容：</strong></p>
<p>{{.Message}}</p>
`

type formData struct {
	Name     string
	Email    string
	Subject  string
	Platform string
	Message  string
}

var platformLabels = map[string]string{
	"windows": "Windows",
	"macos":   "macOS",
	"linux":   "Linux",
	"all":     "全部平台",
}

func platformLabel(v string) string {
	if v == "" {
		return ""
	}
	if label, ok := platformLabels[strings.ToLower(v)]; ok {
		return label
	}
	return v
}

type smtpConfig struct {
	Host string
	Port int
	User string
	Pass string
	To   string
}

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func loadSMTP() smtpConfig {
	port, err := strconv.Atoi(env("SMTP_PORT", "465"))
	if err != nil || port <= 0 {
		port = 465
	}
	return smtpConfig{
		Host: env("SMTP_HOST", "smtp.exmail.qq.com"),
		Port: port,
		User: env("SMTP_USER", ""),
		Pass: env("SMTP_PASS", ""),
		To:   env("MAIL_TO", "info@lanhc.com"),
	}
}

func buildMessage(cfg smtpConfig, data formData) ([]byte, error) {
	var body bytes.Buffer
	tpl, err := template.New("email").Parse(emailBody)
	if err != nil {
		return nil, err
	}
	if err := tpl.Execute(&body, data); err != nil {
		return nil, err
	}

	subject := data.Subject
	if subject == "" {
		subject = "网站表单提交"
	}

	var msg bytes.Buffer
	fmt.Fprintf(&msg, "From: %s\r\n", cfg.User)
	fmt.Fprintf(&msg, "To: %s\r\n", cfg.To)
	fmt.Fprintf(&msg, "Subject: %s\r\n", subject)
	fmt.Fprintf(&msg, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&msg, "Content-Type: text/html; charset=UTF-8\r\n")
	fmt.Fprintf(&msg, "\r\n%s", body.String())
	return msg.Bytes(), nil
}

func sendMail(cfg smtpConfig, msg []byte) error {
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	auth := smtp.PlainAuth("", cfg.User, cfg.Pass, cfg.Host)
	from := cfg.User
	to := []string{cfg.To}

	if cfg.Port == 465 {
		conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: cfg.Host})
		if err != nil {
			return err
		}
		defer conn.Close()
		client, err := smtp.NewClient(conn, cfg.Host)
		if err != nil {
			return err
		}
		defer client.Close()
		if err := client.Auth(auth); err != nil {
			return err
		}
		if err := client.Mail(from); err != nil {
			return err
		}
		for _, rcpt := range to {
			if err := client.Rcpt(rcpt); err != nil {
				return err
			}
		}
		w, err := client.Data()
		if err != nil {
			return err
		}
		if _, err := w.Write(msg); err != nil {
			return err
		}
		if err := w.Close(); err != nil {
			return err
		}
		return client.Quit()
	}

	return smtp.SendMail(addr, auth, from, to, msg)
}

func handleForm(cfg smtpConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.ServeFile(w, r, "src/error.html")
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Error parsing form", http.StatusBadRequest)
			return
		}

		data := formData{
			Email:    r.FormValue("email"),
			Name:     r.FormValue("name"),
			Subject:  r.FormValue("subject"),
			Platform: platformLabel(r.FormValue("platform")),
			Message:  r.FormValue("message"),
		}

		if data.Email == "" || data.Name == "" || data.Message == "" {
			http.Error(w, "missing required form fields", http.StatusBadRequest)
			return
		}

		msg, err := buildMessage(cfg, data)
		if err != nil {
			log.Println("Failed to render message:", err)
			http.ServeFile(w, r, "src/faild.html")
			return
		}
		if err := sendMail(cfg, msg); err != nil {
			log.Println("Failed to send email:", err)
			http.ServeFile(w, r, "src/faild.html")
			return
		}
		http.ServeFile(w, r, "src/success.html")
	}
}

func main() {
	cfg := loadSMTP()

	http.HandleFunc("/", handleForm(cfg))
	http.Handle("/src/", http.StripPrefix("/src/", http.FileServer(http.Dir("src"))))

	addr := env("LISTEN_ADDR", ":80")
	log.Println("lanhc-cf listening on", addr, "smtp:", cfg.Host, cfg.Port, "user:", cfg.User, "to:", cfg.To)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatal(err)
	}
}
