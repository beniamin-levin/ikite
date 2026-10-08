package telegram

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	HTTP   *http.Client
	Token  string
	ChatID string
}

func New(token, chatID string) *Client {
	return &Client{
		HTTP:   &http.Client{Timeout: 20 * time.Second},
		Token:  token,
		ChatID: chatID,
	}
}

func (c *Client) Enabled() bool {
	return c != nil && c.Token != "" && c.ChatID != ""
}

func (c *Client) Send(msg string) error {
	if !c.Enabled() {
		return nil
	}
	endpoint := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", c.Token)
	form := url.Values{}
	form.Set("chat_id", c.ChatID)
	form.Set("text", msg)
	// No link previews: a link such as https://ims.gov.il/en/warnings serves
	// JSON, which Telegram would show as a file attached to the alert.
	form.Set("link_preview_options", `{"is_disabled":true}`)

	resp, err := c.HTTP.Post(endpoint, "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}
