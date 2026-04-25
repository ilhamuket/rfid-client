package main

import (
	"encoding/json"
	"fmt"
	"go.bug.st/serial"
	"log"
	"os"
	"time"
)

type Config struct {
	Port       string `json:"port"`
	BaudRate   int    `json:"baudrate"`
	Endpoint   string `json:"endpoint"`
	Checkpoint string `json:"checkpoint"`
}

func loadConfig() Config {
	file, err := os.Open("config.json")
	if err != nil {
		log.Fatal("Gagal buka config:", err)
	}
	defer file.Close()

	var config Config
	json.NewDecoder(file).Decode(&config)
	return config
}

func main() {
	config := loadConfig()

	fmt.Println("=== RFID CLIENT ===")
	fmt.Println("Checkpoint :", config.Checkpoint)
	fmt.Println("Port       :", config.Port)

	mode := &serial.Mode{
		BaudRate: config.BaudRate,
	}

	for {
		port, err := serial.Open(config.Port, mode)
		if err != nil {
			fmt.Println("Status : DISCONNECTED ❌")
			fmt.Println("Retrying in 3s...")
			time.Sleep(3 * time.Second)
			continue
		}

		fmt.Println("Status : CONNECTED ✅")
		fmt.Println("Waiting for scan...")

		buff := make([]byte, 100)

		for {
			n, err := port.Read(buff)
			if err != nil {
				fmt.Println("Koneksi terputus, reconnecting...")
				port.Close()
				break
			}

			if n > 0 {
				raw := buff[:n]
				fmt.Println("RAW   :", raw)
				fmt.Println("STRING:", string(raw))
			}
		}
	}
}