package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"
)

// ContactLink is one way to reach the platform on the portal's "联系我们"
// page. Value is a link, an address or a number depending on Kind; the
// page turns it into a link where it can and offers copying otherwise.
type ContactLink struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`
	Value string `json:"value"`
	Note  string `json:"note"`
}

// ContactKinds are the kinds of contact a link may be.
var ContactKinds = []string{"telegram", "qq", "wechat", "email", "phone", "discord", "link"}

const (
	maxContactLinks = 20
	maxContactIntro = 500
)

// SetContact replaces the contact page's intro and links.
func (m *Manager) SetContact(ctx context.Context, intro string, links []ContactLink, actorID string) error {
	intro = strings.TrimSpace(intro)
	if utf8.RuneCountInString(intro) > maxContactIntro {
		return fmt.Errorf("%w: 说明最多 %d 字", ErrInvalidSettings, maxContactIntro)
	}
	cleaned, err := cleanContactLinks(links)
	if err != nil {
		return err
	}
	body, err := json.Marshal(cleaned)
	if err != nil {
		return err
	}
	return m.updateLogo(ctx, `contact_intro=$1,contact_links=$2`, "site_contact.updated", actorID, intro, body)
}

func cleanContactLinks(links []ContactLink) ([]ContactLink, error) {
	if len(links) > maxContactLinks {
		return nil, fmt.Errorf("%w: 联系方式最多 %d 条", ErrInvalidSettings, maxContactLinks)
	}
	result := make([]ContactLink, 0, len(links))
	for index, link := range links {
		invalid := func(message string) ([]ContactLink, error) {
			return nil, fmt.Errorf("%w: 第 %d 条联系方式%s", ErrInvalidSettings, index+1, message)
		}
		link.Kind = strings.TrimSpace(link.Kind)
		link.Label = strings.TrimSpace(link.Label)
		link.Value = strings.TrimSpace(link.Value)
		link.Note = strings.TrimSpace(link.Note)
		known := false
		for _, kind := range ContactKinds {
			known = known || kind == link.Kind
		}
		switch {
		case !known:
			return invalid("类型无效")
		case link.Label == "" || link.Value == "":
			return invalid("需要填写名称和内容")
		case utf8.RuneCountInString(link.Label) > 30:
			return invalid("名称最多 30 字")
		case utf8.RuneCountInString(link.Value) > 300:
			return invalid("内容最多 300 字")
		case utf8.RuneCountInString(link.Note) > 100:
			return invalid("备注最多 100 字")
		}
		// Anything that looks like a link must be a web link, so the page
		// never renders a javascript: or other scheme.
		if strings.Contains(link.Value, "://") || strings.HasPrefix(strings.ToLower(link.Value), "javascript:") {
			parsed, err := url.Parse(link.Value)
			if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
				return invalid("的链接需以 http:// 或 https:// 开头")
			}
		}
		if link.Kind == "link" && !strings.HasPrefix(link.Value, "http://") && !strings.HasPrefix(link.Value, "https://") {
			return invalid("的链接需以 http:// 或 https:// 开头")
		}
		result = append(result, link)
	}
	return result, nil
}
