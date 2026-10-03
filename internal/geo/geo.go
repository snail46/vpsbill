// Package geo places a host in a country or territory from what its owner
// wrote about where it is, with the country its IP address belongs to as
// corroboration.
package geo

import (
	"regexp"
	"strings"
)

// place is one country or territory and the pattern that names it: the
// country in Chinese and English, common cities and data-centre sites.
// Its two-letter code is matched separately (see codePattern).
type place struct {
	code    string
	pattern *regexp.Regexp
}

// Names are matched without regard to case. build bounds the Latin ones as
// words, so that "Indiana" is not India; Chinese names need no boundary.
var places = build([][2]string{
	{"HK", `香港|hong\s*kong|hongkong|葵涌|将军澳|將軍澳|荃湾|荃灣|沙田|柴湾|柴灣|hkg|九龙|九龍|kowloon|新界|观塘|觀塘|kwun\s*tong|屯门|屯門|tuen\s*mun|元朗|yuen\s*long|铜锣湾|銅鑼灣|causeway\s*bay|中环|中環|central\s*hk|大埔|tai\s*po|火炭|fo\s*tan|长沙湾|長沙灣|cheung\s*sha\s*wan|葵青|kwai\s*chung|tseung\s*kwan\s*o|tsuen\s*wan|sha\s*tin|chai\s*wan|港岛|港島|西环|西環|鰂鱼涌|鰂魚涌|quarry\s*bay|hkix|hkt|pccw|hgc|wtt|hkbn`},
	{"MO", `澳门|澳門|macau|macao|氹仔|路环|路環|cotai|taipa`},
	{"TW", `台湾|台灣|臺灣|taiwan|台北|臺北|taipei|新北|桃园|桃園|台中|臺中|彰化|高雄|kaohsiung|tpe|新竹|hsinchu|台南|臺南|tainan|基隆|keelung|宜兰|宜蘭|yilan|花莲|花蓮|hualien|changhua|hinet|中华电信|中華電信|chunghwa`},
	{"JP", `日本|japan|东京|東京|tokyo|大阪|osaka|埼玉|saitama|千叶|千葉|chiba|名古屋|nagoya|福冈|福岡|fukuoka|nrt|kix|京都|kyoto|横滨|橫濱|yokohama|神户|神戶|kobe|札幌|sapporo|仙台|sendai|广岛|廣島|hiroshima|冲绳|沖繩|okinawa|那霸|naha|川崎|kawasaki|品川|shinagawa|新宿|shinjuku|涩谷|澀谷|shibuya|石狩|ishikari|软银|軟銀|softbank|\biij\b|ntt|kddi|sakura|樱花|櫻花`},
	{"KR", `韩国|韓國|南韩|south\s*korea|korea|首尔|首爾|seoul|春川|chuncheon|釜山|busan|仁川|incheon|icn|大邱|daegu|光州|gwangju|大田|daejeon|京畿|gyeonggi|安山|ansan|kt\s*idc|sk\s*broadband`},
	{"SG", `新加坡|singapore|狮城|獅城|sgp|裕廊|jurong|樟宜|changi`},
	{"US", `美国|美國|united\s*states|usa|america|洛杉矶|洛杉磯|los\s*angeles|lax|圣何塞|聖何塞|圣荷西|san\s*jose|sjc|硅谷|silicon\s*valley|旧金山|舊金山|三藩市|san\s*francisco|圣克拉拉|santa\s*clara|弗里蒙特|fremont|西雅图|西雅圖|seattle|波特兰|portland|达拉斯|達拉斯|dallas|休斯顿|休斯敦|houston|芝加哥|chicago|纽约|紐約|new\s*york|新泽西|new\s*jersey|阿什本|ashburn|弗吉尼亚|virginia|迈阿密|邁阿密|miami|亚特兰大|亞特蘭大|atlanta|凤凰城|鳳凰城|phoenix|拉斯维加斯|las\s*vegas|丹佛|denver|盐湖城|salt\s*lake|堪萨斯|kansas|水牛城|buffalo|俄勒冈|oregon|加州|加利福尼亚|california|德州|texas|华盛顿|華盛頓|washington|夏威夷|hawaii|波士顿|波士頓|boston|费城|費城|philadelphia|底特律|detroit|明尼阿波利斯|minneapolis|圣路易斯|聖路易斯|st\.?\s*louis|夏洛特|charlotte|里诺|雷诺|reno|萨克拉门托|薩克拉門托|sacramento|圣迭戈|聖迭戈|san\s*diego|奥兰多|奧蘭多|orlando|坦帕|tampa|杰克逊维尔|jacksonville|哥伦布|columbus|克利夫兰|cleveland|匹兹堡|pittsburgh|纳什维尔|nashville|奥斯汀|奧斯汀|austin|圣安东尼奥|san\s*antonio|巴尔的摩|baltimore|希尔斯伯勒|hillsboro|博德曼|boardman|俄亥俄|ohio|犹他|猶他|utah|亚利桑那|arizona|内华达|內華達|nevada|佛罗里达|佛羅里達|florida|伊利诺伊|illinois|密歇根|michigan|宾夕法尼亚|pennsylvania|北卡|north\s*carolina|乔治亚州|喬治亞州|科罗拉多|colorado|密苏里|missouri|明尼苏达|minnesota|美西|美东|美中|美國西岸|us\s*west|us\s*east|us-west|us-east|kansas\s*city|secaucus|piscataway|el\s*segundo|one\s*wilshire`},
	{"CA", `加拿大|canada|多伦多|多倫多|toronto|蒙特利尔|蒙特婁|montreal|温哥华|溫哥華|vancouver|卡尔加里|calgary|渥太华|渥太華|ottawa|博阿努瓦|beauharnois|魁北克|quebec|québec`},
	{"GB", `英国|英國|united\s*kingdom|britain|england|伦敦|倫敦|london|曼彻斯特|manchester|考文垂|coventry|爱丁堡|愛丁堡|edinburgh|伯明翰|birmingham|利兹|leeds|格拉斯哥|glasgow|苏格兰|蘇格蘭|scotland|威尔士|wales|斯劳|slough|maidenhead`},
	{"DE", `德国|德國|germany|deutschland|法兰克福|法蘭克福|frankfurt|纽伦堡|紐倫堡|nuremberg|nürnberg|杜塞尔多夫|düsseldorf|dusseldorf|柏林|berlin|慕尼黑|munich|法尔肯施泰因|falkenstein|汉堡|漢堡|hamburg|科隆|cologne|köln|斯图加特|stuttgart|莱比锡|萊比錫|leipzig|卡尔斯鲁厄|karlsruhe`},
	{"FR", `法国|法國|france|巴黎|paris|马赛|馬賽|marseille|鲁贝|roubaix|斯特拉斯堡|strasbourg|格拉沃利讷|gravelines|里昂|lyon|尼斯|波尔多|波爾多|bordeaux|图卢兹|圖盧茲|toulouse|里尔|lille`},
	{"NL", `荷兰|荷蘭|netherlands|holland|阿姆斯特丹|amsterdam|鹿特丹|rotterdam|海牙|hague|乌得勒支|烏得勒支|utrecht|埃因霍温|eindhoven|naaldwijk|dronten|meppel`},
	{"RU", `俄罗斯|俄羅斯|russia|莫斯科|moscow|圣彼得堡|聖彼得堡|petersburg|伯力|哈巴罗夫斯克|khabarovsk|海参崴|符拉迪沃斯托克|vladivostok|新西伯利亚|novosibirsk|叶卡捷琳堡|葉卡捷琳堡|yekaterinburg|喀山|kazan`},
	{"AU", `澳大利亚|澳大利亞|澳洲|australia|悉尼|雪梨|sydney|墨尔本|墨爾本|melbourne|布里斯班|brisbane|珀斯|perth|阿德莱德|阿德萊德|adelaide|堪培拉|canberra`},
	{"NZ", `新西兰|紐西蘭|新西蘭|new\s*zealand|奥克兰|奧克蘭|auckland`},
	{"IN", `印度|india|孟买|孟買|mumbai|班加罗尔|bangalore|bengaluru|金奈|chennai|新德里|德里|delhi|海得拉巴|hyderabad|加尔各答|kolkata|浦那|pune|诺伊达|noida`},
	{"ID", `印度尼西亚|印度尼西亞|印尼|indonesia|雅加达|雅加達|jakarta`},
	{"MY", `马来西亚|馬來西亞|大马|大馬|malaysia|吉隆坡|kuala\s*lumpur|柔佛|johor|赛城|cyberjaya|槟城|檳城|penang|新山`},
	{"TH", `泰国|泰國|thailand|曼谷|bangkok|清迈|清邁|chiang\s*mai`},
	{"VN", `越南|vietnam|viet\s*nam|胡志明|ho\s*chi\s*minh|河内|河內|hanoi|岘港|峴港|da\s*nang`},
	{"PH", `菲律宾|菲律賓|philippines|马尼拉|馬尼拉|manila|宿务|宿霧|cebu`},
	{"KH", `柬埔寨|cambodia|金边|金邊|phnom\s*penh`},
	{"MM", `缅甸|緬甸|myanmar|仰光|yangon`},
	{"LA", `老挝|寮國|laos|万象|vientiane`},
	{"BD", `孟加拉|bangladesh|达卡|dhaka`},
	{"PK", `巴基斯坦|pakistan|卡拉奇|karachi`},
	{"KZ", `哈萨克斯坦|哈薩克|kazakhstan|阿拉木图|almaty`},
	{"MN", `蒙古国|蒙古國|mongolia|乌兰巴托|ulaanbaatar`},
	{"AE", `阿联酋|阿聯酋|阿拉伯联合酋长国|emirates|uae|迪拜|杜拜|dubai|阿布扎比|abu\s*dhabi|富查伊拉|fujairah`},
	{"SA", `沙特|saudi|利雅得|riyadh|吉达|jeddah`},
	{"TR", `土耳其|turkey|türkiye|turkiye|伊斯坦布尔|伊斯坦堡|istanbul|安卡拉|ankara`},
	{"IL", `以色列|israel|特拉维夫|tel\s*aviv`},
	{"IT", `意大利|義大利|italy|米兰|米蘭|milan|罗马|羅馬|rome|都灵|turin`},
	{"ES", `西班牙|spain|马德里|馬德里|madrid|巴塞罗那|barcelona|瓦伦西亚|valencia`},
	{"PT", `葡萄牙|portugal|里斯本|lisbon`},
	{"CH", `瑞士|switzerland|苏黎世|蘇黎世|zurich|zürich|日内瓦|geneva`},
	{"AT", `奥地利|奧地利|austria|维也纳|維也納|vienna`},
	{"BE", `比利时|比利時|belgium|布鲁塞尔|brussels`},
	{"LU", `卢森堡|盧森堡|luxembourg`},
	{"IE", `爱尔兰|愛爾蘭|ireland|都柏林|dublin`},
	{"SE", `瑞典|sweden|斯德哥尔摩|stockholm|哥德堡|gothenburg|göteborg`},
	{"NO", `挪威|norway|奥斯陆|奧斯陸|oslo`},
	{"FI", `芬兰|芬蘭|finland|赫尔辛基|helsinki`},
	{"DK", `丹麦|丹麥|denmark|哥本哈根|copenhagen`},
	{"IS", `冰岛|冰島|iceland|雷克雅未克|reykjavik`},
	{"PL", `波兰|波蘭|poland|华沙|華沙|warsaw|克拉科夫|krakow|kraków`},
	{"CZ", `捷克|czech|czechia|布拉格|prague`},
	{"HU", `匈牙利|hungary|布达佩斯|budapest`},
	{"RO", `罗马尼亚|羅馬尼亞|romania|布加勒斯特|bucharest`},
	{"BG", `保加利亚|保加利亞|bulgaria|索非亚|sofia`},
	{"GR", `希腊|希臘|greece|雅典|athens`},
	{"UA", `乌克兰|烏克蘭|ukraine|基辅|kyiv|kiev`},
	{"MD", `摩尔多瓦|moldova|基希讷乌|chisinau`},
	{"LT", `立陶宛|lithuania|维尔纽斯|vilnius`},
	{"LV", `拉脱维亚|拉脫維亞|latvia|里加|riga`},
	{"EE", `爱沙尼亚|愛沙尼亞|estonia|塔林|tallinn`},
	{"RS", `塞尔维亚|塞爾維亞|serbia|贝尔格莱德|belgrade`},
	{"BR", `巴西|brazil|brasil|圣保罗|聖保羅|s[aã]o\s*paulo|里约|rio\s*de\s*janeiro|福塔雷萨|fortaleza`},
	{"AR", `阿根廷|argentina|布宜诺斯艾利斯|buenos\s*aires`},
	{"CL", `智利|chile|圣地亚哥|聖地亞哥|santiago`},
	{"MX", `墨西哥|mexico|克雷塔罗|quer[eé]taro`},
	{"CO", `哥伦比亚|哥倫比亞|colombia|波哥大|bogot[aá]`},
	{"ZA", `南非|south\s*africa|约翰内斯堡|johannesburg|开普敦|開普敦|cape\s*town|德班|durban`},
	{"EG", `埃及|egypt|开罗|開羅|cairo`},
	{"NG", `尼日利亚|nigeria|拉各斯|lagos`},
	{"KE", `肯尼亚|kenya|内罗毕|nairobi`},
	{"CN", `中国|中國|大陆|大陸|china|prc|北京|beijing|上海|shanghai|广州|廣州|guangzhou|深圳|shenzhen|杭州|hangzhou|南京|nanjing|成都|chengdu|重庆|重慶|chongqing|武汉|武漢|wuhan|西安|郑州|鄭州|天津|青岛|青島|宁波|寧波|苏州|蘇州|无锡|無錫|镇江|鎮江|扬州|徐州|宿迁|常州|合肥|福州|厦门|廈門|泉州|长沙|長沙|南昌|济南|濟南|沈阳|瀋陽|大连|大連|哈尔滨|长春|石家庄|太原|呼和浩特|昆明|贵阳|貴陽|南宁|海口|兰州|乌鲁木齐|拉萨|东莞|佛山|珠海|惠州|十堰|襄阳|洛阳|枣庄|绍兴|金华|温州|嘉兴|湖州|张家口|乌兰察布|河北|河南|山东|山東|山西|陕西|江苏|江蘇|浙江|安徽|福建|江西|湖北|湖南|广东|廣東|广西|四川|贵州|云南|辽宁|吉林|黑龙江|内蒙|宁夏|甘肃|青海|新疆|西藏|海南`},
})

// codePattern finds two-letter codes written as words in capitals, such as
// the HK in "HK-CN2-01" or the US in "US LAX". Lower case is left alone:
// "us", "in", "it" and "is" are English words.
var codePattern = regexp.MustCompile(`(?:^|[^A-Za-z])([A-Z]{2})(?:[^A-Za-z]|$)`)

// codeAliases are codes people write that mean another: the United
// Kingdom's, and "LA", which is Los Angeles to everyone who sells servers
// and not Laos.
var codeAliases = map[string]string{"UK": "GB", "LA": "US"}

// skippedCodes are capitals that mean a line, a size or a word far more
// often than a country: "CN2" lines, "GB" of disk, "IX" exchanges.
var skippedCodes = map[string]bool{"CN": true, "GB": true, "NO": true, "IN": true, "IT": true, "IS": true, "AT": true, "BE": true}

var known = map[string]bool{}

var latinStart = regexp.MustCompile(`^[a-z\[]`)

func build(table [][2]string) []place {
	result := make([]place, 0, len(table))
	for _, row := range table {
		names := strings.Split(row[1], "|")
		for i, name := range names {
			if latinStart.MatchString(name) {
				names[i] = `\b` + name + `\b`
			}
		}
		result = append(result, place{code: row[0], pattern: regexp.MustCompile(`(?i)` + strings.Join(names, "|"))})
		known[row[0]] = true
	}
	return result
}

// Detect names the country or territory a text is about, or "" when it
// names none. When several are named, the one written first wins, the
// longer name on a tie ("印度尼西亚" over "印度"), and Hong Kong, Macau or
// Taiwan over the mainland wherever they stand ("中国香港").
func Detect(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	best, bestStart, bestLength, special := "", -1, 0, ""
	for _, place := range places {
		span := place.pattern.FindStringIndex(text)
		if span == nil {
			continue
		}
		if special == "" && (place.code == "HK" || place.code == "MO" || place.code == "TW") {
			special = place.code
		}
		if best == "" || span[0] < bestStart || (span[0] == bestStart && span[1]-span[0] > bestLength) {
			best, bestStart, bestLength = place.code, span[0], span[1]-span[0]
		}
	}
	if best == "CN" && special != "" {
		return special
	}
	if best != "" {
		return best
	}
	// No name: a code in capitals. Matches may share a separator ("US-LA"),
	// so look from each position rather than for non-overlapping ones.
	for rest := text; ; {
		match := codePattern.FindStringSubmatchIndex(rest)
		if match == nil {
			return ""
		}
		code := rest[match[2]:match[3]]
		if alias, ok := codeAliases[code]; ok {
			code = alias
		}
		if known[code] && !skippedCodes[rest[match[2]:match[3]]] {
			return code
		}
		rest = rest[match[3]:]
	}
}

// Valid reports whether code is a two-letter country code as an IP
// database gives it; "XX" (unknown) and "T1" (Tor) are not.
func Valid(code string) bool {
	if len(code) != 2 || code == "XX" || code == "ZZ" {
		return false
	}
	for _, letter := range code {
		if letter < 'A' || letter > 'Z' {
			return false
		}
	}
	return true
}

// Placement is where a host is filed in the market.
type Placement struct {
	// Country is the code the host is listed under, "" when nothing tells.
	Country string `json:"country_code"`
	// Source is what told: "location" (the owner's words) or "ip".
	Source string `json:"country_source"`
	// IPCountry is where the host's address is registered, when its agent
	// reported it. It differs from Country when the owner's words say
	// otherwise; the owner's words win, since address databases lag behind
	// where an address is actually announced.
	IPCountry string `json:"ip_country"`
}

// Place files a host: by the location its owner wrote, then by the name of
// its region, then by its address.
func Place(location, region, ipCountry string) Placement {
	ipCountry = strings.ToUpper(strings.TrimSpace(ipCountry))
	if !Valid(ipCountry) {
		ipCountry = ""
	}
	result := Placement{IPCountry: ipCountry}
	for _, text := range []string{location, region} {
		if code := Detect(text); code != "" {
			result.Country, result.Source = code, "location"
			return result
		}
	}
	if ipCountry != "" {
		result.Country, result.Source = ipCountry, "ip"
	}
	return result
}
