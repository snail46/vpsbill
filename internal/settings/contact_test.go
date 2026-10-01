package settings

import (
	"errors"
	"strings"
	"testing"
)

func TestCleanContactLinks(t *testing.T) {
	links, err := cleanContactLinks([]ContactLink{
		{Kind: "telegram", Label: " TG 客服 ", Value: " @vpsbill_support "},
		{Kind: "email", Label: "品牌邮箱", Value: "hi@example.com", Note: "工作日 24 小时内回复"},
		{Kind: "qq", Label: "QQ 群", Value: "123456"},
		{Kind: "link", Label: "官网", Value: "https://example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if links[0].Label != "TG 客服" || links[0].Value != "@vpsbill_support" {
		t.Fatalf("fields are not trimmed: %+v", links[0])
	}
	bad := map[string]ContactLink{
		"unknown kind":      {Kind: "fax", Label: "传真", Value: "1"},
		"empty value":       {Kind: "qq", Label: "QQ 群"},
		"javascript link":   {Kind: "telegram", Label: "TG", Value: "javascript:alert(1)"},
		"other scheme":      {Kind: "qq", Label: "QQ", Value: "ftp://example.com/x"},
		"link without http": {Kind: "link", Label: "官网", Value: "example.com"},
		"long label":        {Kind: "qq", Label: strings.Repeat("长", 31), Value: "1"},
	}
	for name, link := range bad {
		if _, err := cleanContactLinks([]ContactLink{link}); !errors.Is(err, ErrInvalidSettings) {
			t.Errorf("%s: want a validation error, got %v", name, err)
		}
	}
	if _, err := cleanContactLinks(make([]ContactLink, maxContactLinks+1)); !errors.Is(err, ErrInvalidSettings) {
		t.Errorf("too many links: want a validation error, got %v", err)
	}
}
