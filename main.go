package main

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"go.bug.st/serial"
)

type Config struct {
	Port     string `json:"port"`
	BaudRate int    `json:"baudrate"`
}

type TagProfile struct {
	Name  string
	Signs map[string]int
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

func main() {
	cfg := loadConfig()

	fmt.Println("=== RFID TRAINER FINAL V3 ===")
	fmt.Println("5 Tag x 10 Scan")
	fmt.Println()

	mode := &serial.Mode{BaudRate: cfg.BaudRate}

	port, err := serial.Open(cfg.Port, mode)
	if err != nil {
		log.Fatal(err)
	}
	defer port.Close()

	fmt.Println("CONNECTED ✅")

	reader := bufio.NewReader(os.Stdin)

	buf := make([]byte, 256)
	var acc []byte

	for tag := 1; tag <= 5; tag++ {

		fmt.Printf("\n=============================\n")
		fmt.Printf("SIAPKAN TAG-%d\n", tag)
		fmt.Println("Pastikan tag lain jauh dari reader.")
		fmt.Println("Tekan ENTER jika siap.")
		reader.ReadString('\n')

		acc = nil
		flush(port)

		fmt.Println("Mulai dalam:")
		for i := 3; i >= 1; i-- {
			fmt.Println(i)
			time.Sleep(1 * time.Second)
		}

		fmt.Printf("TEMPELKAN TAG-%d SEKARANG\n\n", tag)

		profile := TagProfile{
			Name:  fmt.Sprintf("TAG-%d", tag),
			Signs: map[string]int{},
		}

		total := 0
		lastSig := ""
		lastTime := time.Time{}

		timeout := time.Now().Add(30 * time.Second)

		for total < 10 {

			if time.Now().After(timeout) {
				fmt.Println("Timeout scan.")
				break
			}

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

				// debounce by time
				if sig == lastSig && time.Since(lastTime) < 350*time.Millisecond {
					continue
				}

				lastSig = sig
				lastTime = time.Now()

				total++
				profile.Signs[sig]++

				fmt.Printf("[%d/10] %s\n", total, sig)

				timeout = time.Now().Add(30 * time.Second)

				if total >= 10 {
					break
				}
			}
		}

		printResult(profile)
	}
}

func flush(port serial.Port) {
	tmp := make([]byte, 256)
	until := time.Now().Add(2 * time.Second)

	for time.Now().Before(until) {
		port.Read(tmp)
	}
}

func getSignature(raw string) string {
	if len(raw) <= 16 {
		return raw
	}
	return raw[len(raw)-16:]
}

func printResult(p TagProfile) {

	fmt.Println()
	fmt.Printf("%s RESULT:\n", p.Name)

	keys := sortMap(p.Signs)

	for _, k := range keys {
		fmt.Printf("%s => %dx\n", k, p.Signs[k])
	}

	fmt.Println("-----------------------------")
	fmt.Printf("%s FINAL TOP SIGNATURES:\n", p.Name)

	for i := 0; i < len(keys) && i < 3; i++ {
		fmt.Println(keys[i])
	}

	fmt.Println("-----------------------------")
}

func sortMap(m map[string]int) []string {
	type pair struct {
		Key string
		Val int
	}

	var arr []pair

	for k, v := range m {
		arr = append(arr, pair{k, v})
	}

	sort.Slice(arr, func(i, j int) bool {
		return arr[i].Val > arr[j].Val
	})

	var out []string

	for _, x := range arr {
		out = append(out, x.Key)
	}

	return out
}