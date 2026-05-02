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
	"sync"
	"time"

	"go.bug.st/serial"
)

// ─── ANSI ─────────────────────────────────────────────────────────────────────

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

func clr(c, s string) string { return c + s + colorReset }
func ts() string              { return clr(colorGray, time.Now().Format("15:04:05.000")) }

// ─── CONFIG ───────────────────────────────────────────────────────────────────

type Config struct {
	Ports     []string `json:"ports"`
	Port      string   `json:"port"`
	BaudRate  int      `json:"baudrate"`
	Endpoint  string   `json:"endpoint"`
	DeviceKey string   `json:"device_key"`
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
	if len(c.GetPorts()) == 0 {
		log.Fatal("Tidak ada port dikonfigurasi. Gunakan 'ports' atau 'port' di config.json")
	}
	return c
}

// ─── TAG EXTRACTION — SAMA PERSIS DENGAN main.go ──────────────────────────────
//
// Reader mengirim frame yang mengandung marker "3000" diikuti 12 byte (24 hex char) EPC.
// Contoh raw (hex): ...3000E28011052000603AB12345...
//                         ^^^^^^^^^^^^^^^^^^^^^^^^
//                         12 byte EPC = rfid tag

type tagMatch struct {
	tag    string
	endHex int
}

func extractAllTags(raw string) []tagMatch {
	seen := map[string]bool{}
	var result []tagMatch

	marker := "3000"
	markerLen := len(marker) // 4
	epcLen := 24             // 12 byte = 24 hex char
	idx := 0

	for {
		pos := strings.Index(raw[idx:], marker)
		if pos < 0 {
			break
		}
		abs := idx + pos
		epcStart := abs + markerLen
		epcEnd := epcStart + epcLen
		if epcEnd > len(raw) {
			break
		}
		tag := raw[epcStart:epcEnd]
		if !seen[tag] {
			seen[tag] = true
			result = append(result, tagMatch{tag: tag, endHex: epcEnd})
		}
		idx = epcEnd
	}

	return result
}

// ─── API TYPES ────────────────────────────────────────────────────────────────

type MappingPayload struct {
	ParticipantID int      `json:"participant_id"`
	RfidTags      []string `json:"rfid_tags"`
	Mode          string   `json:"mode"` // "replace" | "append"
	Notes         string   `json:"notes,omitempty"`
}

type MappingResponse struct {
	Success    bool     `json:"success"`
	Message    string   `json:"message"`
	Error      string   `json:"error"`
	ActiveTags []string `json:"active_tags"`
	Conflicts  []struct {
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
	Success    bool `json:"success"`
	Error      string `json:"error"`
	ActiveTags []struct {
		RfidTag    string `json:"rfid_tag"`
		AssignedAt string `json:"assigned_at"`
		Notes      string `json:"notes"`
	} `json:"active_tags"`
	Participant struct {
		Name      string `json:"name"`
		BibNumber string `json:"bib_number"`
	} `json:"participant"`
}

// ─── API HELPERS ──────────────────────────────────────────────────────────────

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
	fmt.Printf("%s%s%s ", clr(colorCyan, "?"), clr(colorBold, " "+label+":"), colorReset)
	line, _ := stdin.ReadString('\n')
	return strings.TrimSpace(line)
}

func promptChoice(label string, choices []string) string {
	for {
		fmt.Printf("%s%s%s [%s] ", clr(colorCyan, "?"), clr(colorBold, " "+label+":"), colorReset, strings.Join(choices, "/"))
		line, _ := stdin.ReadString('\n')
		val := strings.ToLower(strings.TrimSpace(line))
		for _, c := range choices {
			if val == strings.ToLower(c) {
				return val
			}
		}
		fmt.Printf("  %s Pilih salah satu: %s\n", clr(colorRed, "✗"), strings.Join(choices, ", "))
	}
}

func clearScreen() { fmt.Print("\033[H\033[2J") }

func printHeader() {
	fmt.Printf("\n%s%s╔══════════════════════════════════════╗%s\n", colorBold, colorCyan, colorReset)
	fmt.Printf("%s%s║      RFID MAPPING TOOL               ║%s\n", colorBold, colorCyan, colorReset)
	fmt.Printf("%s%s╚══════════════════════════════════════╝%s\n\n", colorBold, colorCyan, colorReset)
}

func printSeparator() {
	fmt.Printf("%s\n", clr(colorGray, "────────────────────────────────────────────────────────"))
}

// portColor memberikan warna unik per port name (sama dengan main.go)
func portColor(portName string) string {
	colors := []string{"\033[35m", "\033[33m", "\033[36m", "\033[32m", "\033[34m"}
	h := 0
	for _, c := range portName {
		h = (h*31 + int(c)) % len(colors)
	}
	return colors[h]
}

// ─── GLOBAL COOLDOWN ─────────────────────────────────────────────────────────
//
// recentlySeen mencegah tag yang sama masuk ke participant berbeda
// hanya karena reader masih memancarkan tag dari scan sebelumnya.
// Tag masuk cooldown selama tagCooldown setelah terdeteksi.

const tagCooldown = 4 * time.Second

var (
	recentlySeen   = map[string]time.Time{}
	recentlySeenMu sync.Mutex
)

func isTagCoolingDown(tag string) bool {
	recentlySeenMu.Lock()
	defer recentlySeenMu.Unlock()
	t, ok := recentlySeen[tag]
	return ok && time.Since(t) < tagCooldown
}

func markTagSeen(tag string) {
	recentlySeenMu.Lock()
	defer recentlySeenMu.Unlock()
	recentlySeen[tag] = time.Now()
}

// ─── SCAN ONE TAG (PER PORT) ──────────────────────────────────────────────────
//
// Membaca dari serial port sampai dapat satu tag baru yang belum di-scan,
// atau user tekan Enter untuk batal.
//
// Mekanisme buffer & parsing IDENTIK dengan runReader() di main.go.

func scanOneTag(serialCh <-chan []byte, portName string, alreadyScanned map[string]bool) (string, bool) {
	pColor := portColor(portName)
	fmt.Printf("  %s[%s]%s %s Dekatkan tag ke reader... %s(Enter = batal)%s\n",
		pColor, portName, colorReset,
		clr(colorYellow, "→"), colorGray, colorReset)

	tagCh  := make(chan string, 1)
	doneCh := make(chan struct{})

	// Goroutine parsing — baca dari channel, bukan langsung dari port
	go func() {
		var acc []byte

		for {
			select {
			case <-doneCh:
				return
			case chunk := <-serialCh:
				acc = append(acc, chunk...)
				if len(acc) > 1024 {
					acc = acc[len(acc)-512:]
				}

				raw := strings.ToUpper(hex.EncodeToString(acc))
				// DEBUG — hapus setelah masalah ketemu
				fmt.Printf("  [DBG] raw(%d bytes): %s\n", len(acc), raw)
				fmt.Printf("  [DBG] contains 3000? %v\n", strings.Contains(raw, "3000"))
				matches := extractAllTags(raw)

				if len(matches) == 0 {
					if len(acc) > 128 {
						acc = acc[len(acc)-32:]
					}
					continue
				}

				// Trim acc sampai setelah byte tag terakhir
				lastEndByte := matches[len(matches)-1].endHex / 2
				if lastEndByte >= len(acc) {
					acc = nil
				} else {
					acc = acc[lastEndByte:]
				}

				for _, m := range matches {
					if alreadyScanned[m.tag] {
						continue
					}
					if isTagCoolingDown(m.tag) {
						fmt.Printf("  %s[%s]%s %s⏳ cooldown%s  %s (tunggu sebentar)\n",
							portColor(portName), portName, colorReset,
							colorGray, colorReset, m.tag)
						continue
					}
					select {
					case tagCh <- m.tag:
					default:
					}
					return
				}
			}
		}
	}()

	// Goroutine baca keyboard — Enter = batal
	cancelCh := make(chan struct{})
	go func() {
		stdin.ReadString('\n')
		select {
		case <-doneCh:
			// sudah selesai, jangan close lagi
		default:
			close(cancelCh)
		}
	}()

	select {
	case tag := <-tagCh:
		close(doneCh)
		markTagSeen(tag) // masuk cooldown global, cegah bleed ke participant berikutnya
		return tag, true
	case <-cancelCh:
		close(doneCh)
		return "", false
	}
}

// ─── PORT SELECTOR ────────────────────────────────────────────────────────────
//
// Kalau hanya ada 1 port di config → langsung pakai.
// Kalau ada beberapa → tampilkan menu pilih port.

func selectPort(cfg Config) (serial.Port, string, error) {
	ports := cfg.GetPorts()

	var chosenPort string
	if len(ports) == 1 {
		chosenPort = ports[0]
	} else {
		fmt.Printf("\n  Port tersedia:\n")
		for i, p := range ports {
			fmt.Printf("    %s%d%s. %s%s%s\n", colorBold, i+1, colorReset, portColor(p), p, colorReset)
		}
		fmt.Println()
		for {
			idxStr := prompt(fmt.Sprintf("Pilih port (1-%d)", len(ports)))
			var idx int
			if _, err := fmt.Sscanf(idxStr, "%d", &idx); err != nil || idx < 1 || idx > len(ports) {
				fmt.Printf("  %s Nomor tidak valid.\n", clr(colorRed, "✗"))
				continue
			}
			chosenPort = ports[idx-1]
			break
		}
	}

	pColor := portColor(chosenPort)
	fmt.Printf("\n  %s Membuka port %s%s%s...\n", ts(), pColor, chosenPort, colorReset)

	mode := &serial.Mode{
		BaudRate: cfg.BaudRate,
	}
	port, err := serial.Open(chosenPort, mode)
	if err != nil {
		return nil, "", fmt.Errorf("gagal buka port %s: %w", chosenPort, err)
	}
	return port, chosenPort, nil
}

// ─── MAIN ─────────────────────────────────────────────────────────────────────

func main() {
	cfg := loadConfig()

	clearScreen()
	printHeader()

	ports := cfg.GetPorts()
	fmt.Printf("  Ports tersedia : %s\n", strings.Join(ports, ", "))
	fmt.Printf("  Endpoint       : %s\n\n", cfg.Endpoint)

	port, portName, err := selectPort(cfg)
	if err != nil {
		log.Fatalf("  %s %v\nPastikan config.json sudah benar.", clr(colorRed, "✗"), err)
	}
	defer port.Close()

	pColor := portColor(portName)
	fmt.Printf("  %s Serial %s%s%s terhubung ✅\n\n", ts(), pColor, portName, colorReset)

	// Goroutine pembaca serial PERMANEN — port.Read() blocking tidak masalah.
	// Data dikirim lewat channel ke scanOneTag, tidak pernah di-stop selama program jalan.
	serialCh := make(chan []byte, 64)
	go func() {
		buf := make([]byte, 256)
		for {
			n, err := port.Read(buf)
			if err != nil {
				fmt.Printf("\n  %s Port error: %v\n", clr(colorRed, "✗"), err)
				return
			}
			if n > 0 {
				tmp := make([]byte, n)
				copy(tmp, buf[:n])
				serialCh <- tmp
			}
		}
	}()

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
			fmt.Printf("  %s ID tidak valid.\n", clr(colorRed, "✗"))
			continue
		}

		// ── 2. Fetch info participant dari server ──────────────────────────
		fmt.Printf("  %s Mengecek participant...\n", ts())
		body, status, err := apiGet(cfg, fmt.Sprintf("/mapping/%d", participantID))
		if err != nil {
			fmt.Printf("  %s Gagal koneksi ke server: %v\n", clr(colorRed, "✗"), err)
			continue
		}
		switch status {
		case 404:
			fmt.Printf("  %s Participant ID %d tidak ditemukan di server.\n",
				clr(colorRed, "✗"), participantID)
			continue
		case 401:
			fmt.Printf("  %s Device key salah. Cek config.json.\n", clr(colorRed, "✗"))
			break mainLoop
		}

		var info ParticipantInfo
		json.Unmarshal(body, &info)

		fmt.Printf("\n  %s  %s  (BIB: %s)\n",
			clr(colorBold, "Participant"),
			clr(colorGreen+colorBold, info.Participant.Name),
			info.Participant.BibNumber,
		)
		if len(info.ActiveTags) > 0 {
			fmt.Printf("  Tag aktif saat ini (%d):\n", len(info.ActiveTags))
			for i, t := range info.ActiveTags {
				fmt.Printf("    %d. %s\n", i+1, clr(colorCyan, t.RfidTag))
			}
		} else {
			fmt.Printf("  %s Belum ada tag terdaftar.\n", clr(colorYellow, "⚠"))
		}
		fmt.Println()

		// ── 3. Pilih aksi ─────────────────────────────────────────────────
		var aksi string
		if len(info.ActiveTags) == 0 {
			aksi = "tambah"
			fmt.Printf("  Mode otomatis: %s (belum ada tag)\n", clr(colorCyan, "tambah"))
		} else {
			fmt.Printf("  %s  Nonaktifkan semua tag lama, scan tag baru\n", clr(colorBold, "ganti  :"))
			fmt.Printf("  %s  Pertahankan tag lama, scan tag tambahan\n",   clr(colorBold, "tambah :"))
			fmt.Printf("  %s  Pilih tag tertentu untuk dinonaktifkan\n",    clr(colorBold, "hapus  :"))
			fmt.Println()
			aksi = promptChoice("Aksi", []string{"ganti", "tambah", "hapus", "batal"})
			if aksi == "batal" {
				fmt.Printf("  %s Dibatalkan.\n", clr(colorYellow, "⚠"))
				continue
			}
		}

		// ── 4a. Mode HAPUS ─────────────────────────────────────────────────
		if aksi == "hapus" {
			if len(info.ActiveTags) == 0 {
				fmt.Printf("  %s Tidak ada tag aktif untuk dihapus.\n", clr(colorYellow, "⚠"))
				continue
			}
			fmt.Println()
			fmt.Printf("  Pilih nomor tag yang ingin dihapus %s(pisah koma, misal: 1,3)%s:\n",
				colorGray, colorReset)
			for i, t := range info.ActiveTags {
				fmt.Printf("    %s%d%s. %s\n", colorBold, i+1, colorReset, clr(colorCyan, t.RfidTag))
			}
			fmt.Println()
			pilihanStr := prompt("Nomor tag")
			if pilihanStr == "" {
				fmt.Printf("  %s Dibatalkan.\n", clr(colorYellow, "⚠"))
				continue
			}

			var tagsToRemove []string
			for _, part := range strings.Split(pilihanStr, ",") {
				var idx int
				if _, err := fmt.Sscanf(strings.TrimSpace(part), "%d", &idx); err != nil {
					continue
				}
				if idx < 1 || idx > len(info.ActiveTags) {
					fmt.Printf("  %s Nomor %d tidak valid, dilewati.\n", clr(colorYellow, "⚠"), idx)
					continue
				}
				tagsToRemove = append(tagsToRemove, info.ActiveTags[idx-1].RfidTag)
			}
			if len(tagsToRemove) == 0 {
				fmt.Printf("  %s Tidak ada tag valid yang dipilih.\n", clr(colorRed, "✗"))
				continue
			}

			fmt.Println()
			printSeparator()
			fmt.Printf("  Participant : %s (ID: %d)\n", clr(colorBold, info.Participant.Name), participantID)
			fmt.Printf("  Aksi        : %s\n", clr(colorRed+colorBold, "HAPUS TAG"))
			fmt.Printf("  Tag (%d)    :\n", len(tagsToRemove))
			for i, t := range tagsToRemove {
				fmt.Printf("    %d. %s\n", i+1, clr(colorYellow, t))
			}
			fmt.Println()
			confirm := promptChoice("Hapus tag ini?", []string{"y", "n"})
			if confirm != "y" {
				fmt.Printf("  %s Dibatalkan.\n", clr(colorYellow, "⚠"))
				continue
			}

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
					fmt.Printf("  %s Gagal hapus %s: %v\n", clr(colorRed, "✗"), tag, err)
					allOK = false
					continue
				}
				var res struct {
					Success bool   `json:"success"`
					Message string `json:"message"`
				}
				json.Unmarshal(respBody, &res)
				if statusCode == 200 && res.Success {
					fmt.Printf("  %s Dihapus: %s\n", clr(colorGreen, "✓"), clr(colorCyan, tag))
				} else {
					fmt.Printf("  %s Gagal hapus %s: %s\n", clr(colorRed, "✗"), tag, res.Message)
					allOK = false
				}
			}
			if allOK {
				fmt.Printf("\n  %s Semua tag berhasil dihapus.\n", clr(colorGreen+colorBold, "✓ SELESAI"))
			} else {
				fmt.Printf("\n  %s Sebagian tag gagal dihapus, cek log di atas.\n", clr(colorYellow, "⚠"))
			}
			fmt.Println()
			continue
		}

		// ── 4b. Mode GANTI / TAMBAH — scan tag dari reader ────────────────
		scannedTags    := map[string]bool{}
		scannedOrdered := []string{}

		fmt.Println()
		fmt.Printf("  %s Scan tag untuk participant ini.\n", clr(colorBold, "SCAN"))
		fmt.Printf("  %s Bisa scan beberapa tag. Tekan %s untuk selesai / stop scan.\n",
			clr(colorGray, "→"), clr(colorBold, "ENTER"))
		fmt.Println()

		for {
			tag, ok := scanOneTag(serialCh, portName, scannedTags)
			if !ok {
				break
			}
			scannedTags[tag] = true
			scannedOrdered = append(scannedOrdered, tag)
			fmt.Printf("  %s Tag #%d: %s\n",
				clr(colorGreen, "✓"), len(scannedOrdered), clr(colorBold+colorCyan, tag))
			fmt.Printf("  %s (scan tag berikutnya, atau Enter untuk simpan)\n\n",
				clr(colorGray, "→"))
		}

		if len(scannedOrdered) == 0 {
			fmt.Printf("  %s Tidak ada tag yang di-scan. Dibatalkan.\n", clr(colorYellow, "⚠"))
			continue
		}

		apiMode := map[string]string{
			"ganti":  "replace",
			"tambah": "append",
		}[aksi]

		// ── 5. Konfirmasi ──────────────────────────────────────────────────
		fmt.Println()
		printSeparator()
		fmt.Printf("  Participant : %s (ID: %d)\n", clr(colorBold, info.Participant.Name), participantID)
		fmt.Printf("  Aksi        : %s\n", clr(colorBold, strings.ToUpper(aksi)))
		fmt.Printf("  Tags (%d)   :\n", len(scannedOrdered))
		for i, t := range scannedOrdered {
			fmt.Printf("    %d. %s\n", i+1, clr(colorCyan, t))
		}
		if aksi == "ganti" && len(info.ActiveTags) > 0 {
			fmt.Printf("  %s Tag lama (%d) akan dinonaktifkan!\n",
				clr(colorYellow, "⚠"), len(info.ActiveTags))
		}
		fmt.Println()
		confirm := promptChoice("Simpan?", []string{"y", "n"})
		if confirm != "y" {
			fmt.Printf("  %s Dibatalkan.\n", clr(colorYellow, "⚠"))
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
			fmt.Printf("  %s Gagal kirim ke server: %v\n", clr(colorRed, "✗"), err)
			continue
		}
		var result MappingResponse
		json.Unmarshal(respBody, &result)

		switch statusCode {
		case 200:
			fmt.Printf("\n  %s %s\n", clr(colorGreen+colorBold, "✓ BERHASIL!"), result.Message)
			fmt.Printf("  Tag aktif sekarang (%d):\n", len(result.ActiveTags))
			for i, t := range result.ActiveTags {
				fmt.Printf("    %d. %s\n", i+1, clr(colorCyan, t))
			}
		case 409:
			fmt.Printf("\n  %s Tag konflik dengan participant lain:\n", clr(colorRed, "✗ KONFLIK!"))
			for _, c := range result.Conflicts {
				fmt.Printf("    • %s → sudah milik: %s (BIB: %s, ID: %d)\n",
					clr(colorYellow, c.Tag), clr(colorBold, c.OwnerName), c.OwnerBib, c.OwnerID)
			}
			fmt.Printf("  %s Hapus tag dari participant lain dulu, atau scan ulang.\n",
				clr(colorYellow, "⚠"))
		case 404:
			fmt.Printf("  %s Participant tidak ditemukan di server.\n", clr(colorRed, "✗"))
		case 401:
			fmt.Printf("  %s Device key salah. Keluar.\n", clr(colorRed, "✗"))
			break mainLoop
		default:
			fmt.Printf("  %s Server error (HTTP %d): %s\n", clr(colorRed, "✗"), statusCode, result.Message)
		}
		fmt.Println()
	}
}