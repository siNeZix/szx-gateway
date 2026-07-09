package firewall

import "regexp"

// Порт HeuristicInjectionClassifier из src/signals/injection.ts (Cerberus).
// 6 паттернов — ловит очевидные injection-фразы без ML.

var injectionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(ignore|disregard|forget)\b[^.!?\n]{0,40}\b(previous|prior|above|earlier|all)\b[^.!?\n]{0,20}\b(instruction|instructions|prompt|prompts|context|rules?)\b`),
	regexp.MustCompile(`(?i)\b(new|updated|revised|the following)\b[^.!?\n]{0,15}\b(instructions?|system prompt|rules?)\b\s*[:-]`),
	regexp.MustCompile(`(?i)\b(reveal|print|show|repeat|disclose|output)\b[^.!?\n]{0,30}\b(system prompt|your instructions|the prompt above)\b`),
	regexp.MustCompile(`(?i)\b(do not|don't|never)\b[^.!?\n]{0,20}\b(tell|inform|alert|notify)\b[^.!?\n]{0,20}\b(the )?(user|human|operator)\b`),
	regexp.MustCompile(`(?i)\byou are now\b[^.!?\n]{0,40}\b(an? )?(unrestricted|jailbroken|developer mode|DAN|different)\b`),
	regexp.MustCompile(`(?i)\b(override|bypass|ignore)\b[^.!?\n]{0,20}\b(safety|security|guardrail|policy|restrictions?)\b`),
}

// ClassifyInjection возвращает score 0 (benign) или 1 (injection) + label.
func ClassifyInjection(text string) (float64, string) {
	for _, re := range injectionPatterns {
		if re.MatchString(text) {
			return 1, "injection"
		}
	}
	return 0, "benign"
}
