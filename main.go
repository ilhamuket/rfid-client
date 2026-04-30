package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.bug.st/serial"
)

// ─── ANSI COLORS ──────────────────────────────────────────────────────────────

const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
	colorCyan   = "\033[36m"
	colorGray   = "\033[90m"
	colorBold   = "\033[1m"
)

func ts() string {
	return colorGray + time.Now().Format("15:04:05.000") + colorReset
}

// ─── CONFIG ───────────────────────────────────────────────────────────────────

type Config struct {
	Ports          []string `json:"ports"`
	Port           string   `json:"port"`
	BaudRate       int      `json:"baudrate"`
	Endpoint       string   `json:"endpoint"`
	DeviceKey      string   `json:"device_key"`
	CheckpointType string   `json:"checkpoint_type"`
	EventID        int      `json:"event_id"`
	DebounceMs     int      `json:"debounce_ms"`
	RetryQueueMax  int      `json:"retry_queue_max"`
	DebugRaw       bool     `json:"debug_raw"`
}

func (c *Config) GetPorts() []string {
	if len(c.Ports) > 0 {
		return c.Ports
	}
	if c.Port != "" {
		return []string{c.Port}
	}
	return nil
}

func loadConfig() Config {
	f, err := os.Open("config.json")
	if err != nil {
		log.Fatal("Tidak bisa buka config.json:", err)
	}
	defer f.Close()

	var c Config
	if err := json.NewDecoder(f).Decode(&c); err != nil {
		log.Fatal("config.json tidak valid:", err)
	}
	if c.DebounceMs == 0 {
		c.DebounceMs = 3000
	}
	if c.RetryQueueMax == 0 {
		c.RetryQueueMax = 100
	}
	validTypes := map[string]bool{"start": true, "finish": true, "checkpoint": true}
	if !validTypes[c.CheckpointType] {
		log.Fatalf("checkpoint_type tidak valid: '%s'", c.CheckpointType)
	}
	if len(c.GetPorts()) == 0 {
		log.Fatal("Tidak ada port dikonfigurasi. Gunakan 'ports' atau 'port' di config.json")
	}
	return c
}

// ─── SCAN PAYLOAD ─────────────────────────────────────────────────────────────

type ScanPayload struct {
	EventID        int    `json:"event_id"`
	CheckpointType string `json:"checkpoint_type"`
	RfidTag        string `json:"rfid_tag"`
	ReaderID       string `json:"reader_id"`
	ScannedAt      string `json:"scanned_at"`
}

// ─── RETRY QUEUE ──────────────────────────────────────────────────────────────

type RetryQueue struct {
	mu    sync.Mutex
	items []ScanPayload
	max   int
}

func (q *RetryQueue) Push(p ScanPayload) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) >= q.max {
		q.items = q.items[1:]
		fmt.Printf("%s %s⚠  Queue penuh (max %d) — scan terlama di-drop%s\n",
			ts(), colorYellow, q.max, colorReset)
	}
	q.items = append(q.items, p)
}

func (q *RetryQueue) Pop() (ScanPayload, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return ScanPayload{}, false
	}
	p := q.items[0]
	q.items = q.items[1:]
	return p, true
}

func (q *RetryQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

// ─── SERVER STATE ─────────────────────────────────────────────────────────────

type ServerState struct {
	down      atomic.Int32
	draining  atomic.Int32
	downSince time.Time
	mu        sync.Mutex
}

func (s *ServerState) MarkDown() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.down.CompareAndSwap(0, 1) {
		s.downSince = time.Now()
		fmt.Printf("%s %s🔴 SERVER DOWN%s — scan baru masuk queue langsung\n",
			ts(), colorRed, colorReset)
	}
}

func (s *ServerState) MarkUp() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.down.CompareAndSwap(1, 0) {
		downFor := time.Since(s.downSince).Round(time.Second)
		fmt.Printf("%s %s🟢 SERVER UP%s — down selama %v\n",
			ts(), colorGreen, colorReset, downFor)
	}
}

func (s *ServerState) IsDown() bool     { return s.down.Load() == 1 }
func (s *ServerState) IsDraining() bool { return s.draining.Load() == 1 }
func (s *ServerState) StartDrain() bool { return s.draining.CompareAndSwap(0, 1) }
func (s *ServerState) StopDrain()       { s.draining.Store(0) }

// ─── SEND RESULT ──────────────────────────────────────────────────────────────

type SendResult int

const (
	SendOK      SendResult = iota
	SendRetry
	SendFatal
	SendInvalid
)

// ─── MAIN ─────────────────────────────────────────────────────────────────────

func main() {
	cfg := loadConfig()
	queue  := &RetryQueue{max: cfg.RetryQueueMax}
	server := &ServerState{}

	hostname, _ := os.Hostname()
	ports := cfg.GetPorts()

	fmt.Printf("\n%s%s=== RFID RACE SCANNER (MULTI-PORT) ===%s\n", colorBold, colorCyan, colorReset)
	fmt.Printf("  Hostname        : %s\n", hostname)
	fmt.Printf("  Ports           : %s\n", strings.Join(ports, ", "))
	fmt.Printf("  Checkpoint Type : %s%s%s\n", colorBold, cfg.CheckpointType, colorReset)
	fmt.Printf("  Event ID        : %d\n", cfg.EventID)
	fmt.Printf("  Endpoint        : %s\n", cfg.Endpoint)
	fmt.Printf("  Debounce        : %dms\n", cfg.DebounceMs)
	fmt.Printf("  Queue Max       : %d\n\n", cfg.RetryQueueMax)

	go func() {
		for {
			time.Sleep(10 * time.Second)
			if queue.Len() == 0 {
				continue
			}
			if !server.StartDrain() {
				continue
			}
			drainQueue(queue, server, cfg)
			server.StopDrain()
		}
	}()

	var wg sync.WaitGroup
	for _, portName := range ports {
		portName := portName
		readerID := fmt.Sprintf("%s-%s", hostname, portName)

		mode := &serial.Mode{BaudRate: cfg.BaudRate}
		port, err := serial.Open(portName, mode)
		if err != nil {
			log.Fatalf("Gagal buka port %s: %v", portName, err)
		}
		fmt.Printf("%s %s✅ Serial port %s terhubung%s  (Reader: %s)\n",
			ts(), colorGreen, portName, colorReset, readerID)

		wg.Add(1)
		go func(p serial.Port, portName, readerID string) {
			defer wg.Done()
			defer p.Close()
			runReader(p, portName, readerID, cfg, queue, server)
		}(port, portName, readerID)
	}
	fmt.Println()
	wg.Wait()
}

// ─── READER LOOP (PER PORT) ───────────────────────────────────────────────────

func runReader(port serial.Port, portName, readerID string, cfg Config, queue *RetryQueue, server *ServerState) {
	buf      := make([]byte, 256)
	var acc  []byte
	lastSeen := map[string]time.Time{}
	pColor   := portColor(portName)
	debugRaw := cfg.DebugRaw

	for {
		n, err := port.Read(buf)
		if err != nil {
			log.Printf("[%s] Read error: %v", portName, err)
			return
		}
		if n == 0 {
			continue
		}

		acc = append(acc, buf[:n]...)
		if len(acc) > 1024 {
			acc = acc[len(acc)-512:]
		}

		raw := strings.ToUpper(hex.EncodeToString(acc))

		if debugRaw {
			fmt.Printf("%s %s[%s]%s RAW(%d) %s\n", ts(), pColor, portName, colorReset, len(acc), raw)
		}

		matches := extractAllTags(raw)

		if len(matches) == 0 {
			if len(acc) > 128 {
				acc = acc[len(acc)-32:]
			}
			continue
		}

		// Trim acc sampai setelah byte tag terakhir yang valid
		lastEndByte := matches[len(matches)-1].endHex / 2
		if lastEndByte >= len(acc) {
			acc = nil
		} else {
			acc = acc[lastEndByte:]
		}

		for _, m := range matches {
			rfidTag := m.tag
			fmt.Printf("%s %s[%s]%s %sdebug: tag → %s%s\n",
				ts(), pColor, portName, colorReset, colorGray, rfidTag, colorReset)

			debounce := time.Duration(cfg.DebounceMs) * time.Millisecond
			if t, ok := lastSeen[rfidTag]; ok {
				if time.Since(t) < debounce {
					fmt.Printf("%s %s[%s]%s %s≈  DEBOUNCE%s  %s  (%.0fms)\n",
						ts(), pColor, portName, colorReset,
						colorGray, colorReset, rfidTag,
						float64(time.Since(t).Milliseconds()))
					continue
				}
			}
			lastSeen[rfidTag] = time.Now()

			payload := ScanPayload{
				EventID:        cfg.EventID,
				CheckpointType: cfg.CheckpointType,
				RfidTag:        rfidTag,
				ReaderID:       readerID,
				ScannedAt:      time.Now().Format("2006-01-02 15:04:05"),
			}

			fmt.Printf("%s %s[%s]%s %s📡 SCAN%s  %s%s%s\n",
				ts(), pColor, portName, colorReset,
				colorBlue, colorReset, colorBold, rfidTag, colorReset)

			if server.IsDown() {
				queue.Push(payload)
				fmt.Printf("%s %s[%s]%s %s📥 QUEUE (server down)%s  %s  → antrian: %d\n",
					ts(), pColor, portName, colorReset,
					colorYellow, colorReset, rfidTag, queue.Len())
				continue
			}

			result := sendScan(payload, cfg, false)
			switch result {
			case SendRetry:
				server.MarkDown()
				queue.Push(payload)
				fmt.Printf("%s %s[%s]%s %s📥 QUEUE%s  %s  → antrian: %d\n",
					ts(), pColor, portName, colorReset,
					colorYellow, colorReset, rfidTag, queue.Len())
			case SendOK:
				if server.IsDown() {
					server.MarkUp()
				}
			}
		}
	}
}

// portColor memberikan warna unik per port name
func portColor(portName string) string {
	colors := []string{"\033[35m", "\033[33m", "\033[36m", "\033[32m", "\033[34m"}
	h := 0
	for _, c := range portName {
		h = (h*31 + int(c)) % len(colors)
	}
	return colors[h]
}

// ─── TAG EXTRACTION ───────────────────────────────────────────────────────────

type tagMatch struct {
	tag    string
	endHex int // posisi akhir dalam string hex (bukan byte)
}

// extractAllTags mem-parse EPC dari raw hex stream.
//
// Struktur frame reader yang terdeteksi dari RAW dump:
//   3000 | EPC (12 byte = 24 hex) | TRAILER (12 byte = 24 hex) | 0CCCFFFF | 20051B00 | 3000 | ...
//
// Byte setelah EPC (trailer 12 byte) kebetulan dimulai dengan E2 80 --
// itulah mengapa parser lama mengira ada "tag kedua" (E280691520...).
// Solusinya: setelah ambil EPC dari posisi "3000", langsung lompat ke
// "3000" berikutnya -- JANGAN scan karakter per karakter dari posisi +1.
func extractAllTags(raw string) []tagMatch {
	seen := map[string]bool{}
	var result []tagMatch

	marker   := "3000"
	markerLen := len(marker) // 4
	epcLen   := 24           // 12 byte EPC = 24 hex chars
	idx      := 0

	for {
		pos := strings.Index(raw[idx:], marker)
		if pos < 0 {
			break
		}
		abs      := idx + pos
		epcStart := abs + markerLen
		epcEnd   := epcStart + epcLen
		if epcEnd > len(raw) {
			break
		}
		tag := raw[epcStart:epcEnd]
		if !seen[tag] {
			seen[tag] = true
			result = append(result, tagMatch{tag: tag, endHex: epcEnd})
		}
		// Lompat ke SETELAH epcEnd -- skip trailer sehingga tidak ada
		// kemunculan E280 dari trailer yang ter-parse sebagai EPC baru.
		idx = epcEnd
	}

	return result
}

// ─── SEND SCAN ────────────────────────────────────────────────────────────────

func sendScan(payload ScanPayload, cfg Config, isRetry bool) SendResult {
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", cfg.Endpoint+"/scan", bytes.NewReader(body))
	if err != nil {
		fmt.Printf("%s %s✗  REQUEST ERROR%s  %s: %v\n", ts(), colorRed, colorReset, payload.RfidTag, err)
		return SendRetry
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DEVICE-KEY", cfg.DeviceKey)

	client  := &http.Client{Timeout: 5 * time.Second}
	start   := time.Now()
	resp, err := client.Do(req)
	latency := time.Since(start).Milliseconds()

	if err != nil {
		fmt.Printf("%s %s✗  NETWORK ERROR%s  %s: %v %s(%dms)%s\n",
			ts(), colorRed, colorReset, payload.RfidTag, err, colorGray, latency, colorReset)
		return SendRetry
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)

	retryMark := ""
	if isRetry {
		retryMark = colorCyan + "↺ " + colorReset
	}

	switch resp.StatusCode {
	case 200:
		success, _ := result["success"].(bool)
		if success {
			participant, hasParticipant := result["participant"].(map[string]interface{})
			timing, hasTiming           := result["timing"].(map[string]interface{})
			checkpoint, _               := result["checkpoint"].(map[string]interface{})

			if hasParticipant && hasTiming {
				bib, _      := participant["bib"]
				name, _     := participant["name"]
				elapsed, _  := timing["elapsed"]
				pos, _      := timing["position"]
				cpName, _   := checkpoint["name"]
				isFinish, _ := result["is_finish"].(bool)
				rawLogId, _ := result["raw_log_id"]

				if isFinish {
					fmt.Printf("%s %s%s%s🏁 FINISH!%s  BIB %-5v  %-20v  %v  pos #%v  raw#%v  %s[%dms]%s\n",
						ts(), retryMark, colorBold+colorGreen, colorReset, colorReset,
						bib, name, elapsed, pos, rawLogId, colorGray, latency, colorReset)
				} else {
					fmt.Printf("%s %s%s✓  OK%s      BIB %-5v  %-20v  elapsed %-10v  pos #%-4v  @ %v  raw#%v  %s[%dms]%s\n",
						ts(), retryMark, colorBold+colorGreen, colorReset,
						bib, name, elapsed, pos, cpName, rawLogId, colorGray, latency, colorReset)
				}
			} else {
				rawLogId, _ := result["raw_log_id"]
				msg, _      := result["message"].(string)
				fmt.Printf("%s %s%s✓  QUEUED%s   raw#%v  %s(%s)%s  %s[%dms]%s\n",
					ts(), retryMark, colorBold+colorGreen, colorReset,
					rawLogId, colorGray, msg, colorReset, colorGray, latency, colorReset)
			}
		} else {
			errCode, _ := result["error"].(string)
			msg, _     := result["message"].(string)

			skipColor := colorGray
			skipIcon  := "→ "
			switch errCode {
			case "already_validated":
				skipColor = colorCyan;   skipIcon = "⊘ "
			case "rapid_duplicate":
				skipColor = colorGray;   skipIcon = "≈ "
			case "unknown_rfid":
				skipColor = colorYellow; skipIcon = "?  "
			case "past_cutoff":
				skipColor = colorYellow; skipIcon = "⏰ "
			case "no_checkpoint_for_category":
				skipColor = colorRed;    skipIcon = "✗  "
			}

			fmt.Printf("%s %s%s%sSKIP%s   %s  %-30s %s(%s)%s  %s[%dms]%s\n",
				ts(), retryMark, skipColor, skipIcon, colorReset,
				payload.RfidTag, errCode, colorGray, msg, colorReset, colorGray, latency, colorReset)
		}
		return SendOK

	case 401:
		fmt.Printf("%s %s✗  UNAUTHORIZED%s — device key salah, cek config.json\n",
			ts(), colorRed, colorReset)
		log.Fatal("Berhenti: unauthorized")
		return SendFatal

	case 422:
		errDetail, _ := result["errors"]
		fmt.Printf("%s %s✗  INVALID REQUEST%s  %s: %v  %s[%dms]%s\n",
			ts(), colorRed, colorReset, payload.RfidTag, errDetail, colorGray, latency, colorReset)
		return SendInvalid

	default:
		fmt.Printf("%s %s✗  SERVER ERROR%s  HTTP %d  %s  %s[%dms]%s\n",
			ts(), colorRed, colorReset, resp.StatusCode, payload.RfidTag, colorGray, latency, colorReset)
		return SendRetry
	}
}

// ─── DRAIN RETRY QUEUE ────────────────────────────────────────────────────────

func drainQueue(queue *RetryQueue, server *ServerState, cfg Config) {
	total   := queue.Len()
	success := 0

	fmt.Printf("%s %s↺  DRAIN MULAI%s  %d item dalam queue\n", ts(), colorCyan, colorReset, total)

	for {
		payload, ok := queue.Pop()
		if !ok {
			break
		}
		result := sendScan(payload, cfg, true)
		switch result {
		case SendOK:
			success++
			if server.IsDown() {
				server.MarkUp()
			}
		case SendInvalid:
			fmt.Printf("%s %s↺  DROP%s  %s @ %s (data invalid)\n",
				ts(), colorYellow, colorReset, payload.RfidTag, payload.ScannedAt)
			success++
		default:
			queue.Push(payload)
			fmt.Printf("%s %s↺  DRAIN BERHENTI%s  server masih down, sisa %d  (berhasil: %d/%d)\n",
				ts(), colorYellow, colorReset, queue.Len(), success, total)
			return
		}
	}

	fmt.Printf("%s %s↺  DRAIN SELESAI%s  %d/%d item terkirim\n",
		ts(), colorGreen, colorReset, success, total)
}