package limits

type Limit struct {
	RPM         int
	RequestsDay int64
	TokensDay   int64
}

const (
	// Источник: https://github.com/cheahjs/free-llm-api-resources, OpenRouter.
	OpenRouterFreeRequestsDay int64 = 50
	AIHubMixFreeRequestsDay   int64 = 10
)

// ponytail: умные per-model лимиты отключены — реальность 10 запросов/аккаунт/сутки
// независимо от модели. Остаётся только RPM (sliding window).
var aihubmixFree = map[string]Limit{
	"gpt-5.5-free":                {RPM: 5},
	"coding-glm-5.2-free":         {RPM: 5},
	"coding-glm-5.1-free":         {RPM: 5},
	"coding-glm-5-free":           {RPM: 5},
	"coding-glm-5-turbo-free":     {RPM: 5},
	"coding-glm-4.7-free":         {RPM: 5},
	"coding-glm-4.6-free":         {RPM: 5},
	"glm-4.7-flash-free":          {RPM: 5},
	"coding-minimax-m3-free":      {RPM: 5},
	"coding-minimax-m2.7-free":    {RPM: 5},
	"coding-minimax-m2.5-free":    {RPM: 5},
	"coding-minimax-m2.1-free":    {RPM: 5},
	"coding-minimax-m2-free":      {RPM: 5},
	"kimi-for-coding-free":        {RPM: 5},
	"k2.6-code-preview-free":      {RPM: 5},
	"xiaomi-mimo-v2.5-pro-free":   {RPM: 5},
	"xiaomi-mimo-v2.5-free":       {RPM: 5},
	"xiaomi-mimo-v2-pro-free":     {RPM: 5},
	"xiaomi-mimo-v2-omni-free":    {RPM: 5},
	"gemini-3-flash-preview-free": {RPM: 5},
	"coding-step-3.7-flash-free":  {RPM: 5},
	"coding-step-3.5-flash-free":  {RPM: 5},
	"step-3.7-flash-free":         {RPM: 10},
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
