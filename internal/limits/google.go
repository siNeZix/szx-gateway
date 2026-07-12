package limits

// GoogleFreeRequestsDay — дефолтный дневной лимит запросов на ключ (free tier).
// ponytail: Google не отдаёт остаток квот по API key. Используем консервативный
// дефолт RPD=1500 (free tier для flash-моделей). Реальный лимит зависит от модели
// и может быть ниже; чекер и proxy корректируют по 429.
const GoogleFreeRequestsDay int64 = 1500

// googleFree — per-model лимиты free tier Google AI Studio.
// Источник: публичная документация Gemini API rate limits (free tier).
// ponytail: значения консервативны, могут меняться Google без уведомления.
// Реальные квоты уточняются по 429; если лимит ниже — cooldown поставит чекер.
var googleFree = map[string]Limit{
	"gemini-2.5-flash":      {RPM: 10, RequestsDay: 500, TokensDay: 0},
	"gemini-2.5-flash-lite": {RPM: 10, RequestsDay: 500, TokensDay: 0},
	"gemini-2.5-pro":        {RPM: 5, RequestsDay: 25, TokensDay: 0},
	"gemini-2.0-flash":      {RPM: 10, RequestsDay: 1500, TokensDay: 0},
	"gemini-2.0-flash-lite": {RPM: 10, RequestsDay: 1500, TokensDay: 0},
	"gemini-1.5-flash":      {RPM: 15, RequestsDay: 1500, TokensDay: 0},
	"gemini-1.5-flash-8b":   {RPM: 15, RequestsDay: 1500, TokensDay: 0},
	"gemini-1.5-pro":        {RPM: 2, RequestsDay: 25, TokensDay: 0},
}

func GoogleFree(model string) (Limit, bool) {
	l, ok := googleFree[model]
	if !ok {
		return Limit{RPM: 10, RequestsDay: 500}, false
	}
	return l, true
}

func GoogleKnownFree() map[string]Limit {
	res := make(map[string]Limit, len(googleFree))
	for k, v := range googleFree {
		res[k] = v
	}
	return res
}
