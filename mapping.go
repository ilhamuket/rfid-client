package main

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"go.bug.st/serial"
)

// ─── ANSI ─────────────────────────────────────────────────────────────────────

const (
	reset  = "\033[0m"
	red    = "\033[31m"
	green  = "\033[32m"
	yellow = "\033[33m"
	cyan   = "\033[36m"
	gray   = "\033[90m"
	bold   = "\033[1m"
)

func clr(c, s string) string { return c + s + reset }
func ts() string              { return clr(gray, time.Now().Format("15:04:05.000")) }

// ─── CONFIG ───────────────────────────────────────────────────────────────────

type Config struct {
	Port      string `json:"port"`
	BaudRate  int    `json:"baudrate"`
	Endpoint  string `json:"endpoint"`
	DeviceKey string `json:"device_key"`
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
	return c
}

// ─── EPC EXTRACTION ───────────────────────────────────────────────────────────

var epcPrefixes = []string{"E280", "E200", "E007"}

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

// ─── API CALLS ────────────────────────────────────────────────────────────────

type MappingPayload struct {
	ParticipantID int      `json:"participant_id"`
	RfidTags      []string `json:"rfid_tags"`
	Mode          string   `json:"mode"` // "replace" | "append"
	Notes         string   `json:"notes,omitempty"`
}

type MappingResponse struct {
	Success     bool     `json:"success"`
	Message     string   `json:"message"`
	Error       string   `json:"error"`
	ActiveTags  []string `json:"active_tags"`
	Conflicts   []struct {
		Tag       string `json:"tag"`
		OwnerID   int    `json:"owner_id"`
		OwnerName string `json:"owner_name"`
		OwnerBib  string `json:"owner_bib"`
	} `json:"conflicts"`
	Participant struct {
		Name      string `json:"name"`
		BibNumber string `json:"bib_number"`
	} `json:"participant"`
}

type ParticipantInfo struct {
	Success     bool     `json:"success"`
	Error       string   `json:"error"`
	ActiveTags  []struct {
		RfidTag    string `json:"rfid_tag"`
		AssignedAt string `json:"assigned_at"`
		Notes      string `json:"notes"`
	} `json:"active_tags"`
	Participant struct {
		Name      string `json:"name"`
		BibNumber string `json:"bib_number"`
	} `json:"participant"`
}

func apiGet(cfg Config, path string) ([]byte, int, error) {
	req, err := http.NewRequest("GET", cfg.Endpoint+path, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("X-DEVICE-KEY", cfg.DeviceKey)
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return body, resp.StatusCode, nil
}

func apiPost(cfg Config, path string, payload interface{}) ([]byte, int, error) {
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", cfg.Endpoint+path, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DEVICE-KEY", cfg.DeviceKey)
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	return respBody, resp.StatusCode, nil
}

func apiDelete(cfg Config, path string, payload interface{}) ([]byte, int, error) {
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest("DELETE", cfg.Endpoint+path, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DEVICE-KEY", cfg.DeviceKey)
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	return respBody, resp.StatusCode, nil
}

// ─── INPUT HELPERS ────────────────────────────────────────────────────────────

var stdin = bufio.NewReader(os.Stdin)

func prompt(label string) string {
	fmt.Printf("%s%s%s ", clr(cyan, "?"), clr(bold, " "+label+":"), reset)
	line, _ := stdin.ReadString('\n')
	return strings.TrimSpace(line)
}

func promptChoice(label string, choices []string) string {
	for {
		fmt.Printf("%s%s%s [%s] ", clr(cyan, "?"), clr(bold, " "+label+":"), reset, strings.Join(choices, "/"))
		line, _ := stdin.ReadString('\n')
		val := strings.ToLower(strings.TrimSpace(line))
		for _, c := range choices {
			if val == strings.ToLower(c) {
				return val
			}
		}
		fmt.Printf("  %s Pilih salah satu: %s\n", clr(red, "✗"), strings.Join(choices, ", "))
	}
}

func clearScreen() {
	fmt.Print("\033[H\033[2J")
}

func printHeader() {
	fmt.Printf("\n%s%s╔══════════════════════════════════════╗%s\n", bold, cyan, reset)
	fmt.Printf("%s%s║      RFID MAPPING TOOL               ║%s\n", bold, cyan, reset)
	fmt.Printf("%s%s╚══════════════════════════════════════╝%s\n\n", bold, cyan, reset)
}

func printSeparator() {
	fmt.Printf("%s%s\n", clr(gray, "────────────────────────────────────────────────────────"), reset)
}

// ─── SCAN LOOP ────────────────────────────────────────────────────────────────
// Baca dari serial port sampai dapat satu tag unik baru, atau user tekan Enter untuk batal.

func scanOneTag(port serial.Port, alreadyScanned map[string]bool) (string, bool) {
	fmt.Printf("  %s Dekatkan tag ke reader... %s(Enter = batal)%s\n",
		clr(yellow, "→"), gray, reset)

	// Channel untuk tag dari serial
	tagCh  := make(chan string, 1)
	doneCh := make(chan struct{})

	// Goroutine baca serial
	go func() {
		buf := make([]byte, 256)
		var acc []byte
		for {
			select {
			case <-doneCh:
				return
			default:
			}
			n, err := port.Read(buf)
			if err != nil || n == 0 {
				continue
			}
			acc = append(acc, buf[:n]...)
			if len(acc) > 1024 {
				acc = acc[len(acc)-512:]
			}
			raw := strings.ToUpper(hex.EncodeToString(acc))
			tags := extractAllTags(raw)
			for _, tag := range tags {
				if !alreadyScanned[tag] {
					acc = nil
					select {
					case tagCh <- tag:
					default:
					}
					return
				}
			}
			if len(tags) > 0 {
				acc = nil
			} else if len(acc) > 128 {
				acc = acc[len(acc)-32:]
			}
		}
	}()

	// Goroutine baca keyboard (Enter = batal)
	cancelCh := make(chan struct{})
	go func() {
		stdin.ReadString('\n')
		close(cancelCh)
	}()

	select {
	case tag := <-tagCh:
		close(doneCh)
		return tag, true
	case <-cancelCh:
		close(doneCh)
		return "", false
	}
}

// ─── MAIN FLOW ────────────────────────────────────────────────────────────────

func main() {
	cfg := loadConfig()

	// Buka serial port
	port, err := serial.Open(cfg.Port, &serial.Mode{BaudRate: cfg.BaudRate})
	if err != nil {
		log.Fatalf("Gagal buka port %s: %v\nPastikan config.json sudah benar.", cfg.Port, err)
	}
	defer port.Close()

	clearScreen()
	printHeader()
	fmt.Printf("  Port     : %s  (%d baud)\n", cfg.Port, cfg.BaudRate)
	fmt.Printf("  Endpoint : %s\n\n", cfg.Endpoint)
	fmt.Printf("  %s Serial terhubung ✅\n\n", ts())

mainLoop:
	for {
		printSeparator()

		// ── 1. Input participant ID ────────────────────────────────────────
		pidStr := prompt("Participant ID (kosongkan untuk keluar)")
		if pidStr == "" {
			fmt.Println("\n  Selesai. Sampai jumpa!")
			break mainLoop
		}

		var participantID int
		if _, err := fmt.Sscanf(pidStr, "%d", &participantID); err != nil || participantID <= 0 {
			fmt.Printf("  %s ID tidak valid.\n", clr(red, "✗"))
			continue
		}

		// ── 2. Fetch info participant dari server ──────────────────────────
		fmt.Printf("  %s Mengecek participant...\n", ts())
		body, status, err := apiGet(cfg, fmt.Sprintf("/mapping/%d", participantID))
		if err != nil {
			fmt.Printf("  %s Gagal koneksi ke server: %v\n", clr(red, "✗"), err)
			continue
		}
		if status == 404 {
			fmt.Printf("  %s Participant ID %d tidak ditemukan di server.\n",
				clr(red, "✗"), participantID)
			continue
		}
		if status == 401 {
			fmt.Printf("  %s Device key salah. Cek config.json.\n", clr(red, "✗"))
			break mainLoop
		}

		var info ParticipantInfo
		json.Unmarshal(body, &info)

		fmt.Printf("\n  %s  %s  (BIB: %s)\n",
			clr(bold, "Participant"),
			clr(green+bold, info.Participant.Name),
			info.Participant.BibNumber,
		)

		if len(info.ActiveTags) > 0 {
			fmt.Printf("  Tag aktif saat ini (%d):\n", len(info.ActiveTags))
			for i, t := range info.ActiveTags {
				fmt.Printf("    %d. %s\n", i+1, clr(cyan, t.RfidTag))
			}
		} else {
			fmt.Printf("  %s Belum ada tag terdaftar.\n", clr(yellow, "⚠"))
		}

		fmt.Println()

		// ── 3. Pilih aksi ─────────────────────────────────────────────────
		var aksi string
		if len(info.ActiveTags) == 0 {
			// Belum ada tag — satu-satunya pilihan adalah tambah
			aksi = "tambah"
			fmt.Printf("  Mode otomatis: %s (belum ada tag)\n", clr(cyan, "tambah"))
		} else {
			fmt.Println()
			fmt.Printf("  %s  Nonaktifkan semua tag lama, scan tag baru\n", clr(bold, "ganti  :"))
			fmt.Printf("  %s  Pertahankan tag lama, scan tag tambahan\n",   clr(bold, "tambah :"))
			fmt.Printf("  %s  Pilih tag tertentu untuk dinonaktifkan\n",    clr(bold, "hapus  :"))
			fmt.Println()
			aksi = promptChoice("Aksi", []string{"ganti", "tambah", "hapus", "batal"})
			if aksi == "batal" {
				fmt.Printf("  %s Dibatalkan.\n", clr(yellow, "⚠"))
				continue
			}
		}

		// ── 4a. Mode HAPUS — pilih tag dari daftar, tanpa scan reader ─────
		if aksi == "hapus" {
			if len(info.ActiveTags) == 0 {
				fmt.Printf("  %s Tidak ada tag aktif untuk dihapus.\n", clr(yellow, "⚠"))
				continue
			}

			fmt.Println()
			fmt.Printf("  Pilih nomor tag yang ingin dihapus %s(pisah koma, misal: 1,3)%s:\n",
				gray, reset)
			for i, t := range info.ActiveTags {
				fmt.Printf("    %s%d%s. %s\n", bold, i+1, reset, clr(cyan, t.RfidTag))
			}
			fmt.Println()

			pilihanStr := prompt("Nomor tag")
			if pilihanStr == "" {
				fmt.Printf("  %s Dibatalkan.\n", clr(yellow, "⚠"))
				continue
			}

			// Parse pilihan nomor
			var tagsToRemove []string
			for _, part := range strings.Split(pilihanStr, ",") {
				var idx int
				if _, err := fmt.Sscanf(strings.TrimSpace(part), "%d", &idx); err != nil {
					continue
				}
				if idx < 1 || idx > len(info.ActiveTags) {
					fmt.Printf("  %s Nomor %d tidak valid, dilewati.\n", clr(yellow, "⚠"), idx)
					continue
				}
				tagsToRemove = append(tagsToRemove, info.ActiveTags[idx-1].RfidTag)
			}

			if len(tagsToRemove) == 0 {
				fmt.Printf("  %s Tidak ada tag valid yang dipilih.\n", clr(red, "✗"))
				continue
			}

			// Konfirmasi
			fmt.Println()
			printSeparator()
			fmt.Printf("  Participant : %s (ID: %d)\n", clr(bold, info.Participant.Name), participantID)
			fmt.Printf("  Aksi        : %s\n", clr(red+bold, "HAPUS TAG"))
			fmt.Printf("  Tag (%d)    :\n", len(tagsToRemove))
			for i, t := range tagsToRemove {
				fmt.Printf("    %d. %s\n", i+1, clr(yellow, t))
			}
			fmt.Println()

			confirm := promptChoice("Hapus tag ini?", []string{"y", "n"})
			if confirm != "y" {
				fmt.Printf("  %s Dibatalkan.\n", clr(yellow, "⚠"))
				continue
			}

			// Kirim DELETE satu per satu ke API
			fmt.Printf("\n  %s Menghapus tag...\n", ts())
			allOK := true
			for _, tag := range tagsToRemove {
				type removePayload struct {
					ParticipantID int    `json:"participant_id"`
					RfidTag       string `json:"rfid_tag"`
				}
				respBody, statusCode, err := apiDelete(cfg, "/mapping", removePayload{
					ParticipantID: participantID,
					RfidTag:       tag,
				})
				if err != nil {
					fmt.Printf("  %s Gagal hapus %s: %v\n", clr(red, "✗"), tag, err)
					allOK = false
					continue
				}
				var res struct {
					Success bool   `json:"success"`
					Message string `json:"message"`
					Error   string `json:"error"`
				}
				json.Unmarshal(respBody, &res)
				if statusCode == 200 && res.Success {
					fmt.Printf("  %s Dihapus: %s\n", clr(green, "✓"), clr(cyan, tag))
				} else {
					fmt.Printf("  %s Gagal hapus %s: %s\n", clr(red, "✗"), tag, res.Message)
					allOK = false
				}
			}

			if allOK {
				fmt.Printf("\n  %s Semua tag berhasil dihapus.\n", clr(green+bold, "✓ SELESAI"))
			} else {
				fmt.Printf("\n  %s Sebagian tag gagal dihapus, cek log di atas.\n", clr(yellow, "⚠"))
			}
			fmt.Println()
			continue
		}

		// ── 4b. Mode GANTI / TAMBAH — scan tag dari reader ────────────────
		scannedTags    := map[string]bool{}
		scannedOrdered := []string{}

		fmt.Println()
		fmt.Printf("  %s Scan tag untuk participant ini.\n", clr(bold, "SCAN"))
		fmt.Printf("  %s Bisa scan beberapa tag. Tekan %s untuk selesai.\n",
			clr(gray, "→"), clr(bold, "ENTER"))
		fmt.Println()

		for {
			tag, ok := scanOneTag(port, scannedTags)
			if !ok {
				break
			}
			scannedTags[tag] = true
			scannedOrdered = append(scannedOrdered, tag)
			fmt.Printf("  %s Tag #%d: %s\n",
				clr(green, "✓"), len(scannedOrdered), clr(bold+cyan, tag))
			fmt.Printf("  %s (scan tag berikutnya, atau Enter untuk simpan)\n\n",
				clr(gray, "→"))
		}

		if len(scannedOrdered) == 0 {
			fmt.Printf("  %s Tidak ada tag yang di-scan. Dibatalkan.\n", clr(yellow, "⚠"))
			continue
		}

		// Map aksi ke mode API
		apiMode := map[string]string{
			"ganti":  "replace",
			"tambah": "append",
		}[aksi]

		// ── 5. Konfirmasi ──────────────────────────────────────────────────
		fmt.Println()
		printSeparator()
		fmt.Printf("  Participant : %s (ID: %d)\n", clr(bold, info.Participant.Name), participantID)
		fmt.Printf("  Aksi        : %s\n", clr(bold, strings.ToUpper(aksi)))
		fmt.Printf("  Tags (%d)   :\n", len(scannedOrdered))
		for i, t := range scannedOrdered {
			fmt.Printf("    %d. %s\n", i+1, clr(cyan, t))
		}
		if aksi == "ganti" && len(info.ActiveTags) > 0 {
			fmt.Printf("  %s Tag lama (%d) akan dinonaktifkan!\n",
				clr(yellow, "⚠"), len(info.ActiveTags))
		}
		fmt.Println()

		confirm := promptChoice("Simpan?", []string{"y", "n"})
		if confirm != "y" {
			fmt.Printf("  %s Dibatalkan.\n", clr(yellow, "⚠"))
			continue
		}

		// ── 6. Kirim ke API ────────────────────────────────────────────────
		fmt.Printf("\n  %s Menyimpan ke server...\n", ts())

		payload := MappingPayload{
			ParticipantID: participantID,
			RfidTags:      scannedOrdered,
			Mode:          apiMode,
		}

		respBody, statusCode, err := apiPost(cfg, "/mapping", payload)
		if err != nil {
			fmt.Printf("  %s Gagal kirim ke server: %v\n", clr(red, "✗"), err)
			continue
		}

		var result MappingResponse
		json.Unmarshal(respBody, &result)

		switch statusCode {
		case 200:
			fmt.Printf("\n  %s %s\n", clr(green+bold, "✓ BERHASIL!"), result.Message)
			fmt.Printf("  Tag aktif sekarang (%d):\n", len(result.ActiveTags))
			for i, t := range result.ActiveTags {
				fmt.Printf("    %d. %s\n", i+1, clr(cyan, t))
			}

		case 409:
			fmt.Printf("\n  %s Tag konflik dengan participant lain:\n", clr(red, "✗ KONFLIK!"))
			for _, c := range result.Conflicts {
				fmt.Printf("    • %s → sudah milik: %s (BIB: %s, ID: %d)\n",
					clr(yellow, c.Tag), clr(bold, c.OwnerName), c.OwnerBib, c.OwnerID)
			}
			fmt.Printf("  %s Hapus tag dari participant lain dulu, atau scan ulang.\n",
				clr(yellow, "⚠"))

		case 404:
			fmt.Printf("  %s Participant tidak ditemukan di server.\n", clr(red, "✗"))

		case 401:
			fmt.Printf("  %s Device key salah. Keluar.\n", clr(red, "✗"))
			break mainLoop

		default:
			fmt.Printf("  %s Server error (HTTP %d): %s\n", clr(red, "✗"), statusCode, result.Message)
		}

		fmt.Println()
	}
}