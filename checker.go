package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"go.bug.st/serial"
)

type Config struct {
	Port     string `json:"port"`
	BaudRate int    `json:"baudrate"`
}

func loadConfig() Config {
	f, err := os.Open("config.json")
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	var c Config
	json.NewDecoder(f).Decode(&c)
	return c
}

var epcPrefixes = []string{"E280", "E200", "E007"}

func main() {
	cfg := loadConfig()

	fmt.Println("=== RFID RACE SCANNER (DEBUG MODE) ===")
	fmt.Println()

	mode := &serial.Mode{BaudRate: cfg.BaudRate}

	port, err := serial.Open(cfg.Port, mode)
	if err != nil {
		log.Fatal(err)
	}
	defer port.Close()

	fmt.Printf("CONNECTED ✅  (port=%s baudrate=%d)\n\n", cfg.Port, cfg.BaudRate)

	buf := make([]byte, 256)
	var acc []byte
	lastSeen := map[string]time.Time{}

	for {
		n, err := port.Read(buf)
		if err != nil {
			log.Fatal(err)
		}
		if n == 0 {
			continue
		}

		acc = append(acc, buf[:n]...)

		// Jaga supaya accumulator tidak tumbuh tak terbatas
		if len(acc) > 1024 {
			acc = acc[len(acc)-512:]
		}

		raw := strings.ToUpper(hex.EncodeToString(acc))

		tags := extractAllTags(raw)
		if len(tags) == 0 {
			// Belum dapat EPC, buang byte lama kalau sudah terlalu panjang
			if len(acc) > 128 {
				acc = acc[len(acc)-32:]
			}
			continue
		}

		// Dapat EPC — tampilkan frame + hasil ekstraksi, lalu reset acc
		debugFrame(raw, tags)
		acc = nil

		// Debounce per tag supaya log tidak banjir
		for _, tag := range tags {
			if t, ok := lastSeen[tag]; ok && time.Since(t) < 1*time.Second {
				fmt.Printf("  ≈ DEBOUNCE  %s  (%.0fms sejak terakhir)\n",
					tag, float64(time.Since(t).Milliseconds()))
				continue
			}
			lastSeen[tag] = time.Now()
			fmt.Printf("  ✅ TAG VALID: %s\n", tag)
		}
	}
}

// extractAllTags mencari semua EPC 12-byte (24 hex char) dalam raw hex string.
// Tidak bergantung pada SOF/EOF byte — langsung cari prefix E280/E200/E007.
func extractAllTags(raw string) []string {
	seen := map[string]bool{}
	var result []string

	for _, prefix := range epcPrefixes {
		idx := 0
		for {
			pos := strings.Index(raw[idx:], prefix)
			if pos < 0 {
				break
			}
			abs := idx + pos
			end := abs + 24
			if end > len(raw) {
				break
			}
			tag := raw[abs:end]
			if !seen[tag] {
				seen[tag] = true
				result = append(result, tag)
			}
			idx = abs + 1
		}
	}
	return result
}

// debugFrame: tampilkan raw hex lengkap + semua EPC yang ditemukan
func debugFrame(raw string, tags []string) {
	fmt.Printf("\n[%s] ┌─ FRAME RECEIVED ──────────────────────────────────────\n", now())
	fmt.Printf("            │ length : %d hex chars (%d bytes)\n", len(raw), len(raw)/2)
	fmt.Printf("            │ raw    : %s\n", raw)
	fmt.Printf("            ├─ KANDIDAT EPC (offset tetap, untuk referensi) ────────\n")

	offsets := []struct {
		start, end int
		label      string
	}{
		{0, 24, "[0:24]   awal frame"},
		{2, 26, "[2:26]   skip 1 byte"},
		{4, 28, "[4:28]   skip 2 byte"},
		{6, 30, "[6:30]   skip 3 byte"},
		{8, 32, "[8:32]   skip 4 byte"},
		{10, 34, "[10:34]  skip 5 byte"},
		{12, 36, "[12:36]  skip 6 byte"},
		{14, 38, "[14:38]  skip 7 byte"},
	}

	for _, c := range offsets {
		slice := safeSlice(raw, c.start, c.end)
		marker := "  "
		for _, p := range epcPrefixes {
			if strings.HasPrefix(slice, p) {
				marker = "★ "
				break
			}
		}
		fmt.Printf("            │ %s%-42s = %s\n", marker, c.label, slice)
	}

	fmt.Printf("            ├─ AUTO-DETECT RESULT ────────────────────────────────\n")
	for _, tag := range tags {
		idx := strings.Index(raw, tag)
		fmt.Printf("            │ ✓ EPC @ index %-3d : %s\n", idx, tag)
	}
	fmt.Printf("            └───────────────────────────────────────────────────────\n")
}

func safeSlice(s string, start, end int) string {
	if start >= len(s) {
		return "(out of range)"
	}
	if end > len(s) {
		end = len(s)
	}
	return s[start:end]
}

func now() string {
	return time.Now().Format("15:04:05")
}