package model

// Preset describes a CMS/service whose traffic can be sent to a dedicated
// outbound. Entries in Domains use the shared rule syntax:
//
//	example.com       -> domain suffix
//	suffix:example.com -> domain suffix
//	full:example.com  -> exact domain
//	keyword:foo       -> substring match
type Preset struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Category    string   `json:"category"`
	Description string   `json:"description"`
	Domains     []string `json:"domains"`
}

var Presets = []Preset{
	{
		ID: "netflix", Name: "Netflix", Category: "streaming",
		Description: "Netflix 播放、图片与视频 CDN",
		Domains: []string{
			"netflix.com", "netflix.net", "nflxext.com", "nflximg.net",
			"nflximg.com", "nflxvideo.net", "nflxso.net", "nflxsearch.net", "fast.com",
		},
	},
	{
		ID: "disney", Name: "Disney+", Category: "streaming",
		Description: "Disney+ 登录、播放与 CDN",
		Domains: []string{
			"disneyplus.com", "disney-plus.net", "disneystreaming.com", "dssott.com",
			"bamgrid.com", "cdn.registerdisney.go.com",
		},
	},
	{
		ID: "hulu", Name: "Hulu", Category: "streaming",
		Description: "Hulu 网页与视频服务",
		Domains:     []string{"hulu.com", "huluim.com", "hulustream.com"},
	},
	{
		ID: "max", Name: "Max / HBO", Category: "streaming",
		Description: "Max（原 HBO Max）",
		Domains:     []string{"max.com", "hbomax.com", "hbo.com", "hbomaxcdn.com"},
	},
	{
		ID: "primevideo", Name: "Prime Video", Category: "streaming",
		Description: "Amazon Prime Video 专用媒体域名",
		Domains: []string{
			"primevideo.com", "amazonvideo.com", "aiv-cdn.net", "aiv-delivery.net",
			"media-amazon.com", "pv-cdn.net",
		},
	},
	{
		ID: "paramount", Name: "Paramount+", Category: "streaming",
		Description: "Paramount+ 流媒体",
		Domains:     []string{"paramountplus.com", "cbsivideo.com", "pplusstatic.com"},
	},
	{
		ID: "peacock", Name: "Peacock", Category: "streaming",
		Description: "NBC Peacock",
		Domains:     []string{"peacocktv.com", "peacock.com"},
	},
	{
		ID: "crunchyroll", Name: "Crunchyroll", Category: "streaming",
		Description: "Crunchyroll / VRV 动漫",
		Domains:     []string{"crunchyroll.com", "vrv.co", "crunchyrollsvc.com"},
	},
	{
		ID: "youtube", Name: "YouTube", Category: "streaming",
		Description: "YouTube 网页、API 与 Google Video CDN",
		Domains: []string{
			"youtube.com", "youtu.be", "ytimg.com", "googlevideo.com",
			"youtube-nocookie.com", "youtubei.googleapis.com",
		},
	},
	{
		ID: "spotify", Name: "Spotify", Category: "streaming",
		Description: "Spotify 登录、播放与图片 CDN",
		Domains:     []string{"spotify.com", "scdn.co", "spotifycdn.com", "spotify.link"},
	},
	{
		ID: "tiktok", Name: "TikTok", Category: "streaming",
		Description: "TikTok 与字节海外 CDN",
		Domains: []string{
			"tiktok.com", "tiktokcdn.com", "tiktokv.com", "ttwstatic.com",
			"muscdn.com", "byteoversea.com",
		},
	},
	{
		ID: "bilibili", Name: "哔哩哔哩", Category: "streaming",
		Description: "B 站港澳台番剧与视频 CDN（按需分流）",
		Domains:     []string{"bilibili.com", "bilibili.tv", "bilivideo.com", "bilivideo.cn", "hdslb.com"},
	},
	{
		ID: "chatgpt", Name: "ChatGPT", Category: "ai",
		Description: "OpenAI / ChatGPT",
		Domains: []string{
			"openai.com", "chatgpt.com", "chatgpt.livekit.cloud",
			"oaistatic.com", "oaiusercontent.com",
		},
	},
	{
		ID: "claude", Name: "Claude", Category: "ai",
		Description: "Anthropic / Claude",
		Domains:     []string{"claude.ai", "anthropic.com", "claudeusercontent.com"},
	},
	{
		ID: "gemini", Name: "Gemini", Category: "ai",
		Description: "Google Gemini / AI Studio",
		Domains: []string{
			"gemini.google.com", "bard.google.com",
			"full:generativelanguage.googleapis.com", "aistudio.google.com",
		},
	},
	{
		ID: "github", Name: "GitHub", Category: "developer",
		Description: "GitHub 网页、原始资源与容器镜像",
		Domains:     []string{"github.com", "githubusercontent.com", "githubassets.com", "ghcr.io", "github.dev"},
	},
	{
		ID: "telegram", Name: "Telegram", Category: "social",
		Description: "Telegram 网页与媒体",
		Domains:     []string{"telegram.org", "t.me", "telegram.me", "telesco.pe"},
	},
}

func PresetByID(id string) (Preset, bool) {
	for _, preset := range Presets {
		if preset.ID == id {
			return preset, true
		}
	}
	return Preset{}, false
}
