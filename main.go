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
	Port           string `json:"port"`
	BaudRate       int    `json:"baudrate"`
	Endpoint       string `json:"endpoint"`
	DeviceKey      string `json:"device_key"`
	CheckpointType string `json:"checkpoint_type"`
	EventID        int    `json:"event_id"`
	DebounceMs     int    `json:"debounce_ms"`
	RetryQueueMax  int    `json:"retry_queue_max"`
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
		log.Fatalf("checkpoint_type tidak valid: '%s'. Harus: start, finish, atau checkpoint", c.CheckpointType)
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

// ─── RETRY QUEUE (thread-safe) ────────────────────────────────────────────────

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
// Lacak apakah server sedang reachable atau tidak.
// Kalau serverDown == 1, scan baru langsung masuk queue tanpa coba kirim.
// Reset ke 0 saat drain berhasil kirim minimal satu item.

type ServerState struct {
	down         atomic.Int32  // 0 = up, 1 = down
	draining     atomic.Int32  // 0 = idle, 1 = sedang drain
	downSince    time.Time
	mu           sync.Mutex
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

func (s *ServerState) IsDown() bool  { return s.down.Load() == 1 }
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
	readerID := fmt.Sprintf("%s-%s", hostname, cfg.Port)

	fmt.Printf("\n%s%s=== RFID RACE SCANNER ===%s\n", colorBold, colorCyan, colorReset)
	fmt.Printf("  Reader ID       : %s\n", readerID)
	fmt.Printf("  Checkpoint Type : %s%s%s\n", colorBold, cfg.CheckpointType, colorReset)
	fmt.Printf("  Event ID        : %d\n", cfg.EventID)
	fmt.Printf("  Endpoint        : %s\n", cfg.Endpoint)
	fmt.Printf("  Debounce        : %dms\n", cfg.DebounceMs)
	fmt.Printf("  Queue Max       : %d\n\n", cfg.RetryQueueMax)

	mode := &serial.Mode{BaudRate: cfg.BaudRate}
	port, err := serial.Open(cfg.Port, mode)
	if err != nil {
		log.Fatal("Gagal buka port:", err)
	}
	defer port.Close()

	fmt.Printf("%s %s✅ Serial port %s terhubung%s\n\n", ts(), colorGreen, cfg.Port, colorReset)

	// ── Background drain goroutine ─────────────────────────────────────────
	// Drain jalan sendiri, tidak blocking scanner.
	// Coba drain setiap 10 detik kalau queue tidak kosong.
	go func() {
		for {
			time.Sleep(10 * time.Second)

			if queue.Len() == 0 {
				continue
			}

			// Jangan mulai drain kedua kalau yang pertama masih jalan
			if !server.StartDrain() {
				continue
			}

			drainQueue(queue, server, cfg)
			server.StopDrain()
		}
	}()

	// ── Serial read loop ───────────────────────────────────────────────────
	buf := make([]byte, 256)
	var acc []byte
	lastSeen := map[string]time.Time{}

	for {
		n, err := port.Read(buf)
		if err != nil {
			log.Fatal("Read error:", err)
		}
		if n == 0 {
			continue
		}

		acc = append(acc, buf[:n]...)

		for {
			sof := bytes.IndexByte(acc, 0xE0)
			if sof < 0 {
				acc = nil
				break
			}
			acc = acc[sof:]
			if len(acc) < 2 {
				break
			}

			next := bytes.IndexByte(acc[1:], 0xE0)
			if next < 0 {
				break
			}

			frame := acc[1 : next+1]
			acc = acc[next+1:]

			raw := strings.ToUpper(hex.EncodeToString(frame))
			rfidTag := extractTag(raw)

			if rfidTag == "" {
				fmt.Printf("%s %sdebug: frame kosong, raw=%s%s\n", ts(), colorGray, raw, colorReset)
				continue
			}

			// Log SEBELUM debounce check — untuk tahu tag apa yang dibaca hardware
			fmt.Printf("%s %sdebug: raw frame → tag=%s%s\n", ts(), colorGray, rfidTag, colorReset)

			// ── Local debounce ─────────────────────────────────────────────
			debounce := time.Duration(cfg.DebounceMs) * time.Millisecond
			if t, ok := lastSeen[rfidTag]; ok {
				if time.Since(t) < debounce {
					fmt.Printf("%s %s≈  DEBOUNCE%s  %s  (%.0fms sejak terakhir)\n",
						ts(), colorGray, colorReset,
						rfidTag,
						float64(time.Since(t).Milliseconds()),
					)
					continue
				}
			}
			lastSeen[rfidTag] = time.Now()

			scannedAt := time.Now().Format("2006-01-02 15:04:05")

			payload := ScanPayload{
				EventID:        cfg.EventID,
				CheckpointType: cfg.CheckpointType,
				RfidTag:        rfidTag,
				ReaderID:       readerID,
				ScannedAt:      scannedAt,
			}

			fmt.Printf("%s %s📡 SCAN%s  %s%s%s\n",
				ts(), colorBlue, colorReset,
				colorBold, rfidTag, colorReset,
			)

			// ── Kalau server diketahui down, langsung queue ────────────────
			// Tidak perlu buang waktu 5 detik timeout.
			if server.IsDown() {
				queue.Push(payload)
				fmt.Printf("%s %s📥 QUEUE (server down)%s  %s  → antrian: %d item\n",
					ts(), colorYellow, colorReset, rfidTag, queue.Len())
				continue
			}

			// ── Kirim langsung ─────────────────────────────────────────────
			result := sendScan(payload, cfg, false)

			switch result {
			case SendRetry:
				server.MarkDown()
				queue.Push(payload)
				fmt.Printf("%s %s📥 QUEUE%s  %s  → antrian: %d item\n",
					ts(), colorYellow, colorReset, rfidTag, queue.Len())
			case SendOK:
				// Kalau sebelumnya down dan sekarang bisa kirim, mark up
				if server.IsDown() {
					server.MarkUp()
				}
			}
		}
	}
}

// ─── SEND SCAN ────────────────────────────────────────────────────────────────

func sendScan(payload ScanPayload, cfg Config, isRetry bool) SendResult {
	body, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", cfg.Endpoint+"/scan", bytes.NewReader(body))
	if err != nil {
		fmt.Printf("%s %s✗  REQUEST ERROR%s  %s: %v\n",
			ts(), colorRed, colorReset, payload.RfidTag, err)
		return SendRetry
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DEVICE-KEY", cfg.DeviceKey)

	client := &http.Client{Timeout: 5 * time.Second}

	start := time.Now()
	resp, err := client.Do(req)
	latency := time.Since(start).Milliseconds()

	if err != nil {
		fmt.Printf("%s %s✗  NETWORK ERROR%s  %s: %v %s(%dms)%s\n",
			ts(), colorRed, colorReset,
			payload.RfidTag, err,
			colorGray, latency, colorReset,
		)
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
        // Cek apakah response punya data participant (sync) atau hanya acknowledgement (queue)
        participant, hasParticipant := result["participant"].(map[string]interface{})
        timing, hasTiming           := result["timing"].(map[string]interface{})
        checkpoint, _               := result["checkpoint"].(map[string]interface{})

        if hasParticipant && hasTiming {
            // Response lengkap — sync processing (atau job selesai sangat cepat)
            bib, _      := participant["bib"]
            name, _     := participant["name"]
            elapsed, _  := timing["elapsed"]
            pos, _      := timing["position"]
            cpName, _   := checkpoint["name"]
            isFinish, _ := result["is_finish"].(bool)
            rawLogId, _ := result["raw_log_id"]

            if isFinish {
                fmt.Printf("%s %s%s🏁 FINISH!%s  BIB %-5v  %-20v  %v  pos #%v  raw#%v  %s[%dms]%s\n",
                    ts(), retryMark, colorBold+colorGreen, colorReset,
                    bib, name, elapsed, pos, rawLogId,
                    colorGray, latency, colorReset,
                )
            } else {
                fmt.Printf("%s %s%s✓  OK%s      BIB %-5v  %-20v  elapsed %-10v  pos #%-4v  @ %v  raw#%v  %s[%dms]%s\n",
                    ts(), retryMark, colorBold+colorGreen, colorReset,
                    bib, name, elapsed, pos, cpName, rawLogId,
                    colorGray, latency, colorReset,
                )
            }
        } else {
            // Queue acknowledgement — scan diterima, akan diproses background
            rawLogId, _ := result["raw_log_id"]
            msg, _      := result["message"].(string)
            fmt.Printf("%s %s%s✓  QUEUED%s   raw#%v  %s(%s)%s  %s[%dms]%s\n",
                ts(), retryMark, colorBold+colorGreen, colorReset,
                rawLogId,
                colorGray, msg, colorReset,
                colorGray, latency, colorReset,
            )
        }

    } else {
        // Skip normal
        errCode, _ := result["error"].(string)
        msg, _     := result["message"].(string)

        skipColor := colorGray
        skipIcon  := "→ "
        switch errCode {
        case "already_validated":
            skipColor = colorCyan
            skipIcon  = "⊘ "
        case "rapid_duplicate":
            skipColor = colorGray
            skipIcon  = "≈ "
        case "unknown_rfid":
            skipColor = colorYellow
            skipIcon  = "?  "
        case "past_cutoff":
            skipColor = colorYellow
            skipIcon  = "⏰ "
        case "no_checkpoint_for_category":
            skipColor = colorRed
            skipIcon  = "✗  "
        }

        fmt.Printf("%s %s%s%sSKIP%s   %s  %-30s %s(%s)%s  %s[%dms]%s\n",
            ts(), retryMark,
            skipColor, skipIcon, colorReset,
            payload.RfidTag,
            errCode,
            colorGray, msg, colorReset,
            colorGray, latency, colorReset,
        )
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
			ts(), colorRed, colorReset,
			payload.RfidTag, errDetail,
			colorGray, latency, colorReset,
		)
		return SendInvalid

	default:
		fmt.Printf("%s %s✗  SERVER ERROR%s  HTTP %d  %s  %s[%dms]%s\n",
			ts(), colorRed, colorReset,
			resp.StatusCode, payload.RfidTag,
			colorGray, latency, colorReset,
		)
		return SendRetry
	}
}

// ─── DRAIN RETRY QUEUE ────────────────────────────────────────────────────────

func drainQueue(queue *RetryQueue, server *ServerState, cfg Config) {
	total   := queue.Len()
	success := 0

	fmt.Printf("%s %s↺  DRAIN MULAI%s  %d item dalam queue\n",
		ts(), colorCyan, colorReset, total)

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
			// Data salah, buang saja (tidak perlu retry)
			fmt.Printf("%s %s↺  DROP%s  %s @ %s (data invalid, dibuang)\n",
				ts(), colorYellow, colorReset,
				payload.RfidTag, payload.ScannedAt,
			)
			success++ // anggap selesai

		default:
			// Server masih down — kembalikan ke queue dan berhenti
			queue.Push(payload)
			fmt.Printf("%s %s↺  DRAIN BERHENTI%s  server masih down, sisa %d item  (berhasil: %d/%d)\n",
				ts(), colorYellow, colorReset,
				queue.Len(), success, total,
			)
			return
		}
	}

	fmt.Printf("%s %s↺  DRAIN SELESAI%s  %d/%d item terkirim\n",
		ts(), colorGreen, colorReset, success, total)
}

// ─── TAG EXTRACTION ───────────────────────────────────────────────────────────

func extractTag(raw string) string {
	if len(raw) < 16 {
		return ""
	}
	return raw[len(raw)-16:]
}