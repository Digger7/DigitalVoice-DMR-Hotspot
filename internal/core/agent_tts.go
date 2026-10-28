package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"dmr-hotspot/internal/logx"
)

// PiperTTS synthesises speech with the piper CLI and returns 8 kHz s16le PCM.
type PiperTTS struct {
	Voice       string
	Binary      string
	VoiceDir    string
	LengthScale float64
	Rate        float64
}

func NewPiperTTS(voice, binary, voiceDir string, lengthScale, rate float64) *PiperTTS {
	if lengthScale <= 0 {
		lengthScale = 1.0
	}
	if rate <= 0 {
		rate = 1.0
	}
	return &PiperTTS{Voice: voice, Binary: binary, VoiceDir: voiceDir, LengthScale: lengthScale, Rate: rate}
}

// effectiveLengthScale converts the speech-rate multiplier into piper's
// length scale: higher rate means shorter phonemes (faster speech).
func (p *PiperTTS) effectiveLengthScale() float64 {
	ls := p.LengthScale
	if ls <= 0 {
		ls = 1.0
	}
	rate := p.Rate
	if rate <= 0 {
		rate = 1.0
	}
	return ls / rate
}

func (p *PiperTTS) findBinary() (string, error) {
	candidates := []string{p.Binary, os.Getenv("DMR_PIPER")}
	for _, root := range searchRoots() {
		candidates = append(candidates,
			filepath.Join(root, "piper"),
			filepath.Join(root, ".venv", "bin", "piper"),
		)
	}
	candidates = append(candidates, "piper")
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if strings.ContainsRune(c, os.PathSeparator) || filepath.IsAbs(c) {
			if st, err := os.Stat(c); err == nil && !st.IsDir() {
				return c, nil
			}
			continue
		}
		if found, err := exec.LookPath(c); err == nil {
			return found, nil
		}
	}
	return "", fmt.Errorf("piper binary not found (install piper-tts or set agent.piper_binary)")
}

func (p *PiperTTS) voiceDirs() []string {
	dirs := []string{p.VoiceDir, os.Getenv("DMR_VOICES")}
	for _, root := range searchRoots() {
		dirs = append(dirs,
			filepath.Join(root, "voices", "piper"),
			filepath.Join(root, "voices"),
		)
	}
	out := make([]string, 0, len(dirs))
	for _, d := range dirs {
		if d != "" {
			out = append(out, d)
		}
	}
	return out
}

// walkVoices scans every voice directory recursively so models can be kept
// in per-language subfolders (voices/piper/ru/ru_RU/...).
func walkVoices(dirs []string, fn func(name, path string)) {
	seen := map[string]bool{}
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		abs, err := filepath.Abs(dir)
		if err != nil || seen[abs] {
			continue
		}
		seen[abs] = true
		_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if !strings.HasSuffix(strings.ToLower(d.Name()), ".onnx") {
				return nil
			}
			name := strings.TrimSuffix(d.Name(), filepath.Ext(d.Name()))
			fn(name, path)
			return nil
		})
	}
}

func (p *PiperTTS) resolveVoice() (string, error) {
	name := p.Voice
	if name == "" {
		name = "ru_RU-denis-medium"
	}
	if strings.HasSuffix(strings.ToLower(name), ".onnx") {
		if st, err := os.Stat(name); err == nil && !st.IsDir() {
			return name, nil
		}
	}
	if strings.ContainsRune(name, os.PathSeparator) {
		for _, suffix := range []string{"", ".onnx"} {
			if st, err := os.Stat(name + suffix); err == nil && !st.IsDir() {
				return name + suffix, nil
			}
		}
	}
	var found string
	walkVoices(p.voiceDirs(), func(n, path string) {
		if found == "" && strings.EqualFold(n, name) {
			found = path
		}
	})
	if found != "" {
		return found, nil
	}
	return "", fmt.Errorf("piper voice %q not found (searched %v)", name, p.voiceDirs())
}

func voiceSampleRate(model string) int {
	data, err := os.ReadFile(model + ".json")
	if err == nil {
		var cfg struct {
			Audio struct {
				SampleRate int `json:"sample_rate"`
			} `json:"audio"`
		}
		if json.Unmarshal(data, &cfg) == nil && cfg.Audio.SampleRate > 0 {
			return cfg.Audio.SampleRate
		}
	}
	return 22050
}

// Speak returns 8 kHz mono s16le PCM ready for the vocoder.
func (p *PiperTTS) Speak(text string) ([]byte, error) {
	type result struct {
		pcm []byte
		err error
	}
	ch := make(chan result, 1)
	go func() {
		pcm, err := p.synthesise(text)
		ch <- result{pcm, err}
	}()
	select {
	case r := <-ch:
		return r.pcm, r.err
	case <-time.After(60 * time.Second):
		return nil, fmt.Errorf("piper timed out")
	}
}

func (p *PiperTTS) synthesise(text string) ([]byte, error) {
	binary, err := p.findBinary()
	if err != nil {
		return nil, err
	}
	model, err := p.resolveVoice()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	args := []string{"--model", model, "--output-raw",
		"--length-scale", fmt.Sprintf("%.2f", p.effectiveLengthScale()),
		"--noise-scale", "0.55", "--noise-w-scale", "0.55",
		"--sentence-silence", "0.35"}
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Stdin = strings.NewReader(strings.TrimSpace(text) + "\n")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("piper failed: %s", msg)
	}
	rate := voiceSampleRate(model)
	pcm := stdout.Bytes()
	logx.GetLogger("agent").Debugf("TTS: %d bytes @ %d Hz", len(pcm), rate)
	if rate != 8000 {
		pcm = ResampleAntiAliased(pcm, rate, 8000)
	}
	return pcm, nil
}

type voiceInfo struct {
	name     string
	path     string
	language string
	quality  string
	speaker  string
	label    string
}

// parseVoiceName extracts language, quality and speaker from common Piper
// names such as ru_RU-irina-medium or en_US-libritts-high.
func parseVoiceName(base string) (language, quality, speaker string) {
	parts := strings.Split(base, "-")
	if len(parts) < 2 {
		return "", "", base
	}
	if strings.Contains(parts[0], "_") {
		language = parts[0]
	}
	for i := len(parts) - 1; i >= 1; i-- {
		lp := strings.ToLower(parts[i])
		switch {
		case strings.HasPrefix(lp, "high"):
			quality = "high"
		case strings.HasPrefix(lp, "medium"):
			quality = "medium"
		case strings.HasPrefix(lp, "low"):
			quality = "low"
		}
		if quality != "" {
			speaker = strings.Join(parts[1:i], "-")
			break
		}
	}
	if speaker == "" {
		if language != "" {
			speaker = strings.Join(parts[1:], "-")
		} else {
			speaker = base
		}
	}
	return language, quality, speaker
}

func voiceQualityRank(q string) int {
	switch q {
	case "high":
		return 0
	case "medium":
		return 1
	case "low":
		return 2
	default:
		return 3
	}
}

func voiceLanguageRank(lang string) int {
	if strings.HasPrefix(strings.ToLower(lang), "ru") {
		return 0
	}
	return 1
}

func humanizeSpeaker(s string) string {
	s = strings.ReplaceAll(s, "_", " ")
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = []rune(strings.ToUpper(string(r[0])))[0]
	return string(r)
}

// ListPiperVoices returns every reachable *.onnx voice with a friendly label,
// sorted by language, quality and name.
func ListPiperVoices(voiceDir string) []map[string]string {
	p := NewPiperTTS("", "", voiceDir, 1, 1)
	seen := map[string]voiceInfo{}
	walkVoices(p.voiceDirs(), func(name, path string) {
		if _, ok := seen[name]; ok {
			return
		}
		lang, quality, speaker := parseVoiceName(name)
		label := humanizeSpeaker(speaker)
		var tags []string
		if lang != "" {
			tags = append(tags, lang)
		}
		if quality != "" {
			tags = append(tags, quality)
		}
		if len(tags) > 0 {
			label += " · " + strings.Join(tags, " · ")
		}
		seen[name] = voiceInfo{name: name, path: path, language: lang, quality: quality, speaker: speaker, label: label}
	})
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		a, b := seen[names[i]], seen[names[j]]
		if la, lb := voiceLanguageRank(a.language), voiceLanguageRank(b.language); la != lb {
			return la < lb
		}
		if qa, qb := voiceQualityRank(a.quality), voiceQualityRank(b.quality); qa != qb {
			return qa < qb
		}
		return names[i] < names[j]
	})
	out := make([]map[string]string, 0, len(names))
	for _, name := range names {
		v := seen[name]
		out = append(out, map[string]string{
			"name": v.name, "path": v.path, "label": v.label,
			"language": v.language, "quality": v.quality, "speaker": v.speaker,
		})
	}
	return out
}
