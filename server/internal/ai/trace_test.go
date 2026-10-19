package ai

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestChatTraceAccumulation(t *testing.T) {
	tr := NewChatTrace("abcd1234", "public")
	tr.SetIntro(6, 2400, 80, 2)
	tr.SetGuideCtx(350)

	tr.AddLLM(420, 3800, 5200, 2100, 1200, 300)
	tr.AddLLM(300, 2500, 6000, 900, 1400, 120)
	tr.AddRetry()
	tr.AddTool("guide_search", 350)
	tr.AddTool("guide_search", 120) // dédoublonné dans ToolsUsed
	tr.AddTruncation()
	tr.MarkForcedFinal()
	tr.SetGuideMs(6200)
	tr.SetMapMs(3100)
	tr.SetMapResult(3, 2)
	tr.SetZone("Besançon", "base", 14)
	tr.MarkNearbyFallback(30)
	tr.SetOutput(240, 21500, 2100, 430, 440, false)
	time.Sleep(5 * time.Millisecond)
	tr.Finish()

	snap := tr.Snapshot()
	if snap.LLMCalls != 2 || snap.TokensIn != 2600 || snap.TokensOut != 420 {
		t.Fatalf("appels LLM : %+v", snap)
	}
	if snap.ToolCallsCount != 2 || len(snap.ToolsUsed) != 1 || snap.ToolMs != 470 {
		t.Fatalf("outils : %+v", snap)
	}
	if snap.TTFTMs != 420 { // premier appel uniquement
		t.Fatalf("ttft = %d", snap.TTFTMs)
	}
	if snap.Truncations != 1 || !snap.ForcedFinal || snap.LLMRetries != 1 {
		t.Fatalf("replis/bornes : %+v", snap)
	}
	if snap.MapNames != 3 || snap.MapMatched != 2 {
		t.Fatalf("passe carte : %+v", snap)
	}
	if snap.Zone != "Besançon" || snap.ZoneSource != "base" || snap.ZoneInside != 14 {
		t.Fatalf("zone : %+v", snap)
	}
	if !snap.NearbyFallback || snap.NearbyRadiusKm != 30 {
		t.Fatalf("repli proche : %+v", snap)
	}
	if snap.GuideMs != 6200 || snap.MapMs != 3100 || snap.TotalMs <= 0 {
		t.Fatalf("chronométrie : %+v", snap)
	}

	// La trace sérialise proprement pour le JSONL.
	raw, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"trace_id", "tokens_in", "ttft_ms", "guide_pass_ms"} {
		if !strings.Contains(string(raw), `"`+key+`"`) {
			t.Fatalf("clé %s absente du JSON : %s", key, raw)
		}
	}
}

func TestTraceFileRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "traces", "chat.jsonl")
	if err := SetTraceFile(path); err != nil {
		t.Fatal(err)
	}
	defer SetTraceFile("")

	tr := NewChatTrace("deadbeef", "pro")
	tr.SetOutput(3, 12, 45, 10, 11, false)
	WriteTrace(tr)
	WriteTrace(NewChatTrace("cafebabe", "public"))

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("lignes = %d, want 2", len(lines))
	}
	var got ChatTraceData
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatal(err)
	}
	if got.TraceID != "deadbeef" || got.Space != "pro" {
		t.Fatalf("trace lue = %+v", got)
	}
}

func TestExcerptOptIn(t *testing.T) {
	t.Setenv("AI_LOG_CONTENT", "")
	if Excerpt("bonjour", 100) != "" {
		t.Fatal("contenu loggé sans opt-in")
	}
	t.Setenv("AI_LOG_CONTENT", "1")
	if got := Excerpt("bonjour le monde", 8); got != "bonjour …" {
		t.Fatalf("extrait = %q", got)
	}
}
