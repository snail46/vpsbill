package geo

import "testing"

func TestDetect(t *testing.T) {
	for text, want := range map[string]string{
		"香港 葵涌":             "HK",
		"中国香港":              "HK",
		"香港（中国）":            "HK",
		"美国 洛杉矶":            "US",
		"Los Angeles, CA":   "US",
		"LA":                "US",
		"US LAX":            "US",
		"US-LA":             "US",
		"日本东京":              "JP",
		"Tokyo":             "JP",
		"JP":                "JP",
		"新加坡":               "SG",
		"SG-01":             "SG",
		"印度尼西亚 雅加达":         "ID",
		"印度 孟买":             "IN",
		"Indiana":           "",
		"Romania Bucharest": "RO",
		"罗马尼亚":              "RO",
		"西班牙 马德里":           "ES",
		"马德里":               "ES",
		"台湾 台北":             "TW",
		"中国台湾":              "TW",
		"韩国首尔":              "KR",
		"德国 法兰克福":           "DE",
		"英国伦敦":              "GB",
		"UK London":         "GB",
		"UK":                "GB",
		"澳门":                "MO",
		"澳大利亚 悉尼":           "AU",
		"上海":                "CN",
		"中国 广东 深圳":          "CN",
		"三网 CN2 GIA":        "",
		"100 GB":            "",
		"BGP":               "",
		"it is in us":       "",
		"月球":                "",
		"":                  "",
		"俄罗斯 伯力":            "RU",
		"Netherlands":       "NL",
		"越南 胡志明":            "VN",
		"南非":                "ZA",
		"阿联酋 迪拜":            "AE",
		"老挝 万象":             "LA",
		// Cities and sites belong to their country; Hong Kong, Macau and
		// Taiwan are their own.
		"纽约": "US", "New York": "US", "西雅图": "US", "芝加哥": "US", "达拉斯": "US", "阿什本": "US", "圣何塞 CN2": "US", "Kansas City": "US", "波士顿": "US", "美西 9929": "US",
		"东京": "JP", "大阪 软银": "JP", "Osaka": "JP", "京都": "JP", "横滨": "JP", "札幌": "JP",
		"首尔": "KR", "春川": "KR", "釜山": "KR",
		"九龙": "HK", "新界 屯门": "HK", "长沙湾": "HK", "Kowloon": "HK", "HKBN": "HK",
		"氹仔": "MO", "Taipa": "MO",
		"台北 HiNet": "TW", "新竹": "TW", "台中": "TW",
		"长沙": "CN", "法兰克福": "DE", "阿姆斯特丹": "NL", "伦敦": "GB", "悉尼": "AU", "多伦多": "CA", "吉隆坡": "MY", "胡志明市": "VN",
	} {
		if got := Detect(text); got != want {
			t.Errorf("Detect(%q) = %q, want %q", text, got, want)
		}
	}
}

func TestPlace(t *testing.T) {
	for _, c := range []struct {
		location, region, ip string
		want                 Placement
	}{
		{"香港 葵涌", "亚太", "HK", Placement{"HK", "location", "HK"}},
		{"香港 葵涌", "亚太", "us", Placement{"HK", "location", "US"}},
		{"机房 A 区", "日本", "", Placement{"JP", "location", ""}},
		{"机房 A 区", "默认", "SG", Placement{"SG", "ip", "SG"}},
		{"机房 A 区", "默认", "XX", Placement{}},
		{"机房 A 区", "默认", "T1", Placement{}},
		{"美国 洛杉矶", "香港", "", Placement{"US", "location", ""}},
	} {
		if got := Place(c.location, c.region, c.ip); got != c.want {
			t.Errorf("Place(%q, %q, %q) = %+v, want %+v", c.location, c.region, c.ip, got, c.want)
		}
	}
}
