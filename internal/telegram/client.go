// Package telegram runs the site's Telegram bot: customers link their
// Telegram account to their site account, then earn balance in the site's
// group by checking in and by inviting members.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultAPIBase is Telegram's Bot API; a site that cannot reach it may
// point the bot at a reverse proxy instead.
const DefaultAPIBase = "https://api.telegram.org"

// pollSeconds is how long Telegram holds a getUpdates request open.
const pollSeconds = 30

// Client calls the Bot API for one bot.
type Client struct {
	base  string
	token string
	http  *http.Client
}

func NewClient(apiBase, token string) *Client {
	base := strings.TrimRight(strings.TrimSpace(apiBase), "/")
	if base == "" {
		base = DefaultAPIBase
	}
	return &Client{base: base, token: token, http: &http.Client{Timeout: (pollSeconds + 15) * time.Second}}
}

// APIError is an answer Telegram marked as failed.
type APIError struct {
	Code        int
	Description string
}

func (e *APIError) Error() string { return fmt.Sprintf("telegram: %s (%d)", e.Description, e.Code) }

// Forbidden reports that the bot may not do this: it cannot write to a
// user who never started it, or lacks a right in the group.
func Forbidden(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && (apiErr.Code == http.StatusForbidden || apiErr.Code == http.StatusBadRequest)
}

func (c *Client) call(ctx context.Context, method string, params any, result any) error {
	body, err := json.Marshal(params)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/bot"+c.token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		// The URL carries the token; keep it out of logs.
		return errors.New("telegram: " + method + " request failed: " + strings.ReplaceAll(err.Error(), c.token, "***"))
	}
	defer response.Body.Close()
	var envelope struct {
		OK          bool            `json:"ok"`
		Description string          `json:"description"`
		ErrorCode   int             `json:"error_code"`
		Result      json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&envelope); err != nil {
		return fmt.Errorf("telegram: %s answered %d", method, response.StatusCode)
	}
	if !envelope.OK {
		return &APIError{Code: envelope.ErrorCode, Description: envelope.Description}
	}
	if result == nil {
		return nil
	}
	return json.Unmarshal(envelope.Result, result)
}

type User struct {
	ID           int64  `json:"id"`
	IsBot        bool   `json:"is_bot"`
	FirstName    string `json:"first_name"`
	Username     string `json:"username"`
	LanguageCode string `json:"language_code"`
}

type Chat struct {
	ID       int64  `json:"id"`
	Type     string `json:"type"`
	Title    string `json:"title"`
	Username string `json:"username"`
}

type Message struct {
	MessageID      int64  `json:"message_id"`
	From           *User  `json:"from"`
	Chat           Chat   `json:"chat"`
	Text           string `json:"text"`
	NewChatMembers []User `json:"new_chat_members"`
	LeftChatMember *User  `json:"left_chat_member"`
}

type ChatMember struct {
	Status string `json:"status"`
	User   User   `json:"user"`
	// IsMember is set for restricted members who are still in the chat.
	IsMember         bool `json:"is_member"`
	CanInviteUsers   bool `json:"can_invite_users"`
	CanDeleteMessage bool `json:"can_delete_messages"`
}

// Present reports whether the member is in the chat.
func (m ChatMember) Present() bool {
	switch m.Status {
	case "creator", "administrator", "member":
		return true
	case "restricted":
		return m.IsMember
	}
	return false
}

type ChatMemberUpdated struct {
	Chat          Chat       `json:"chat"`
	From          User       `json:"from"`
	OldChatMember ChatMember `json:"old_chat_member"`
	NewChatMember ChatMember `json:"new_chat_member"`
	InviteLink    *struct {
		InviteLink string `json:"invite_link"`
	} `json:"invite_link"`
}

type Update struct {
	UpdateID     int64              `json:"update_id"`
	Message      *Message           `json:"message"`
	ChatMember   *ChatMemberUpdated `json:"chat_member"`
	MyChatMember *ChatMemberUpdated `json:"my_chat_member"`
	Callback     *CallbackQuery     `json:"callback_query"`
}

func (c *Client) GetMe(ctx context.Context) (User, error) {
	var user User
	return user, c.call(ctx, "getMe", struct{}{}, &user)
}

// GetUpdates waits for updates from offset on. chat_member has to be
// asked for by name; Telegram leaves it out otherwise.
func (c *Client) GetUpdates(ctx context.Context, offset int64) ([]Update, error) {
	var updates []Update
	err := c.call(ctx, "getUpdates", map[string]any{
		"offset": offset, "timeout": pollSeconds, "allowed_updates": []string{"message", "chat_member", "my_chat_member", "callback_query"},
	}, &updates)
	return updates, err
}

// SendMessage sends HTML text; replyTo, when not 0, makes it a reply.
func (c *Client) SendMessage(ctx context.Context, chatID int64, html string, replyTo int64) (Message, error) {
	params := map[string]any{"chat_id": chatID, "text": html, "parse_mode": "HTML", "link_preview_options": map[string]any{"is_disabled": true}}
	if replyTo != 0 {
		params["reply_parameters"] = map[string]any{"message_id": replyTo, "allow_sending_without_reply": true}
	}
	var message Message
	return message, c.call(ctx, "sendMessage", params, &message)
}

func (c *Client) DeleteMessage(ctx context.Context, chatID, messageID int64) error {
	return c.call(ctx, "deleteMessage", map[string]any{"chat_id": chatID, "message_id": messageID}, nil)
}

// GetChat looks a chat up by its numeric id or its @username.
func (c *Client) GetChat(ctx context.Context, chat any) (Chat, error) {
	var result Chat
	return result, c.call(ctx, "getChat", map[string]any{"chat_id": chat}, &result)
}

func (c *Client) GetChatMember(ctx context.Context, chatID, userID int64) (ChatMember, error) {
	var member ChatMember
	return member, c.call(ctx, "getChatMember", map[string]any{"chat_id": chatID, "user_id": userID}, &member)
}

// CreateInviteLink makes a named invite link; members who join through it
// are reported with it (see ChatMemberUpdated.InviteLink).
func (c *Client) CreateInviteLink(ctx context.Context, chatID int64, name string) (string, error) {
	var link struct {
		InviteLink string `json:"invite_link"`
	}
	err := c.call(ctx, "createChatInviteLink", map[string]any{"chat_id": chatID, "name": name}, &link)
	return link.InviteLink, err
}

// Button is one button under a message: it opens URL, or sends Data back
// to the bot as a callback.
type Button struct {
	Text string `json:"text"`
	URL  string `json:"url,omitempty"`
	Data string `json:"data,omitempty"`
}

// CallbackQuery is a press of a Data button.
type CallbackQuery struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message"`
	Data    string   `json:"data"`
}

func keyboard(buttons [][]Button) map[string]any {
	rows := make([][]map[string]string, 0, len(buttons))
	for _, row := range buttons {
		items := make([]map[string]string, 0, len(row))
		for _, button := range row {
			item := map[string]string{"text": button.Text}
			if button.URL != "" {
				item["url"] = button.URL
			} else {
				item["callback_data"] = button.Data
			}
			items = append(items, item)
		}
		rows = append(rows, items)
	}
	return map[string]any{"inline_keyboard": rows}
}

// SendButtons sends HTML text with buttons under it.
func (c *Client) SendButtons(ctx context.Context, chatID int64, html string, buttons [][]Button) (Message, error) {
	params := map[string]any{"chat_id": chatID, "text": html, "parse_mode": "HTML", "link_preview_options": map[string]any{"is_disabled": true}}
	if len(buttons) > 0 {
		params["reply_markup"] = keyboard(buttons)
	}
	var message Message
	return message, c.call(ctx, "sendMessage", params, &message)
}

// EditMessage replaces a message's text and buttons (none removes them).
func (c *Client) EditMessage(ctx context.Context, chatID, messageID int64, html string, buttons [][]Button) error {
	return c.call(ctx, "editMessageText", map[string]any{
		"chat_id": chatID, "message_id": messageID, "text": html, "parse_mode": "HTML",
		"link_preview_options": map[string]any{"is_disabled": true}, "reply_markup": keyboard(buttons),
	}, nil)
}

// AnswerCallback ends the wait shown on a pressed button; text, when not
// empty, pops up for the user.
func (c *Client) AnswerCallback(ctx context.Context, id, text string) error {
	return c.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": id, "text": text, "show_alert": text != ""}, nil)
}
