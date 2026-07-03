package limits

type Limit struct {
	RPM         int
	RequestsDay int64
	TokensDay   int64
}

var aihubmixFree = map[string]Limit{
	"gpt-5.5-free":                {RPM: 5, RequestsDay: 500, TokensDay: 1000000},
	"coding-glm-5.2-free":         {RPM: 5, RequestsDay: 500, TokensDay: 1000000},
	"coding-glm-5.1-free":         {RPM: 5, RequestsDay: 500, TokensDay: 1000000},
	"coding-glm-5-free":           {RPM: 5, RequestsDay: 500, TokensDay: 1000000},
	"coding-glm-5-turbo-free":     {RPM: 5, RequestsDay: 500, TokensDay: 1000000},
	"coding-glm-4.7-free":         {RPM: 5, RequestsDay: 500, TokensDay: 1000000},
	"coding-glm-4.6-free":         {RPM: 5, RequestsDay: 500, TokensDay: 1000000},
	"glm-4.7-flash-free":          {RPM: 5, RequestsDay: 500, TokensDay: 1000000},
	"coding-minimax-m3-free":      {RPM: 5, RequestsDay: 500, TokensDay: 1000000},
	"coding-minimax-m2.7-free":    {RPM: 5, RequestsDay: 500, TokensDay: 1000000},
	"coding-minimax-m2.5-free":    {RPM: 5, RequestsDay: 500, TokensDay: 1000000},
	"coding-minimax-m2.1-free":    {RPM: 5, RequestsDay: 500, TokensDay: 1000000},
	"coding-minimax-m2-free":      {RPM: 5, RequestsDay: 500, TokensDay: 1000000},
	"kimi-for-coding-free":        {RPM: 5, RequestsDay: 500, TokensDay: 1000000},
	"k2.6-code-preview-free":      {RPM: 5, RequestsDay: 500, TokensDay: 1000000},
	"xiaomi-mimo-v2.5-pro-free":   {RPM: 5, RequestsDay: 500, TokensDay: 1000000},
	"xiaomi-mimo-v2.5-free":       {RPM: 5, RequestsDay: 500, TokensDay: 1000000},
	"xiaomi-mimo-v2-pro-free":     {RPM: 5, RequestsDay: 500, TokensDay: 1000000},
	"xiaomi-mimo-v2-omni-free":    {RPM: 5, RequestsDay: 500, TokensDay: 1000000},
	"gemini-3-flash-preview-free": {RPM: 5, RequestsDay: 250, TokensDay: 500000},
	"coding-step-3.7-flash-free":  {RPM: 5, RequestsDay: 250, TokensDay: 500000},
	"coding-step-3.5-flash-free":  {RPM: 5, RequestsDay: 250, TokensDay: 500000},
	"step-3.7-flash-free":         {RPM: 10, RequestsDay: 200, TokensDay: 2000000},
}

func AIHubMixFree(model string) (Limit, bool) {
	l, ok := aihubmixFree[model]
	if !ok {
		// ponytail: unknown free models still get the documented default RPM; no fake daily cap.
		return Limit{RPM: 5}, false
	}
	return l, true
}

func AIHubMixKnownFree() map[string]Limit {
	res := make(map[string]Limit, len(aihubmixFree))
	for k, v := range aihubmixFree {
		res[k] = v
	}
	return res
}
