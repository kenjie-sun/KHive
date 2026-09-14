package notify

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"

	"github.com/1239t/vohive/internal/config"
)

type EmailChannel struct {
	cfg config.EmailConfig
}

func NewEmailChannel(cfg config.EmailConfig) (*EmailChannel, error) {
	if cfg.SMTPHost == "" || cfg.SMTPPort == 0 || cfg.FromAddress == "" || len(cfg.ToAddresses) == 0 {
		return nil, errors.New("email configuration is incomplete")
	}
	return &EmailChannel{cfg: cfg}, nil
}

func (c *EmailChannel) Name() string {
	return "email"
}

func (c *EmailChannel) Send(text string) error {
	return c.SendWithContext(NotificationContext{Event: "通知", Text: text})
}

func (c *EmailChannel) SendWithContext(ctx NotificationContext) error {
	auth := smtp.PlainAuth("", c.cfg.Username, c.cfg.Password, c.cfg.SMTPHost)
	addr := fmt.Sprintf("%s:%d", c.cfg.SMTPHost, c.cfg.SMTPPort)

	subject := fmt.Sprintf("[Vohive] %s", ctx.Event)
	if label := ctx.DeviceLabel(); label != "未知设备" {
		subject = fmt.Sprintf("[Vohive] %s - %s", ctx.Event, label)
	}

	to := strings.Join(c.cfg.ToAddresses, ",")
	msg := []byte(fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s\r\n", c.cfg.FromAddress, to, subject, ctx.Text))

	// Bound the complete SMTP conversation so one stalled server cannot keep
	// its durable channel worker occupied indefinitely.
	connection, err := net.DialTimeout("tcp", addr, 15*time.Second)
	if err != nil {
		return err
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(60 * time.Second))
	if c.cfg.UseSSL {
		secured := tls.Client(connection, &tls.Config{ServerName: c.cfg.SMTPHost})
		if err := secured.Handshake(); err != nil {
			return err
		}
		connection = secured
	}
	client, err := smtp.NewClient(connection, c.cfg.SMTPHost)
	if err != nil {
		return err
	}
	defer client.Close()
	if !c.cfg.UseSSL {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(&tls.Config{ServerName: c.cfg.SMTPHost}); err != nil {
				return err
			}
		}
	}
	if ok, _ := client.Extension("AUTH"); ok {
		if err := client.Auth(auth); err != nil {
			return err
		}
	}
	if err := client.Mail(c.cfg.FromAddress); err != nil {
		return err
	}
	for _, recipient := range c.cfg.ToAddresses {
		if err := client.Rcpt(recipient); err != nil {
			return err
		}
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := writer.Write(msg); err != nil {
		_ = writer.Close()
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	_ = client.Quit()

	return nil
}

func (c *EmailChannel) RegisterCommand(cmd string, handler CommandHandler) {
	// 邮件渠道不支持接收指令
}

func (c *EmailChannel) Start() error {
	return nil
}

func (c *EmailChannel) Close() error {
	return nil
}
