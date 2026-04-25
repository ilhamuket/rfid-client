package main

import (
	"bytes"
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

type Runner struct {
	Bib   string
	Name  string
	Signs []string
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

// SAMPLE DATABASE
var runners = []Runner{
	{"101", "Peserta A", []string{"9E6660000066607E"}},
	{"102", "Peserta B", []string{"9E666000006618FE"}},
	{"103", "Peserta C", []string{"9E66600000661886", "98187E1880601880"}},
	{"104", "Peserta D", []string{"9E66600000061878", "98187E188060187E"}},
	{"105", "Peserta E", []string{"66600000666098FE"}},
}

func main() {
	cfg := loadConfig()

	fmt.Println("=== RFID RACE SCANNER ===")
	fmt.Println("ACTIVE MODE")
	fmt.Println()

	mode := &serial.Mode{
		BaudRate: cfg.BaudRate,
	}

	port, err := serial.Open(cfg.Port, mode)
	if err != nil {
		log.Fatal(err)
	}
	defer port.Close()

	fmt.Println("CONNECTED ✅")

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
			sig := getSignature(raw)

			runner, found := findRunner(sig)
			if !found {
				fmt.Printf("[%s] UNKNOWN %s\n",
					now(),
					sig,
				)
				continue
			}

			// anti duplicate 3 detik
			if t, ok := lastSeen[runner.Bib]; ok {
				if time.Since(t) < 3*time.Second {
					continue
				}
			}

			lastSeen[runner.Bib] = time.Now()

			fmt.Printf("[%s] BIB:%s | %s | %s\n",
				now(),
				runner.Bib,
				runner.Name,
				sig,
			)

			// TODO kirim ke Laravel API
		}
	}
}

func getSignature(raw string) string {
	if len(raw) <= 16 {
		return raw
	}
	return raw[len(raw)-16:]
}

func findRunner(sig string) (Runner, bool) {
	for _, r := range runners {
		for _, s := range r.Signs {
			if s == sig {
				return r, true
			}
		}
	}
	return Runner{}, false
}

func now() string {
	return time.Now().Format("15:04:05")
}