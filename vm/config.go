package vm

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"

	"github.com/hashicorp/go-hclog"
	"github.com/lab47/isle/pkg/bytesize"
)

type Config struct {
	Cores      int    `json:"cores"`
	Memory     string `json:"ram"`
	Swap       string `json:"swap"`
	DataSize   string `json:"system_disk"`
	UserSize   string `json:"user_disk"`
	MacAddress string `json:"mac_address"`
	ClusterId  string `json:"unique_id"`
}

func newUniqueId() string {
	data := make([]byte, 5)
	io.ReadFull(rand.Reader, data)

	return hex.EncodeToString(data)
}

func CheckConfig(log hclog.Logger, configPath string) error {
	f, err := os.Open(configPath)
	if err != nil {
		f, err := os.Create(configPath)
		if err != nil {
			return fmt.Errorf("unable to create default config: %w", err)
		}

		enc := json.NewEncoder(f)
		enc.SetIndent("", "  ")

		mac := make(net.HardwareAddr, 6)

		_, err = io.ReadFull(rand.Reader, mac)
		if err != nil {
			return err
		}

		// Make sure that the local administered bits are set
		mac[0] = mac[0] & 0b11111110

		enc.Encode(Config{
			Cores:      0,
			DataSize:   "100G",
			UserSize:   "100G",
			MacAddress: mac.String(),
			ClusterId:  newUniqueId(),
		})
		f.Close()
	} else {
		defer f.Close()

		var cfg Config
		err = json.NewDecoder(f).Decode(&cfg)
		if err != nil {
			return fmt.Errorf("error parsing config: %w", err)
		}

		if cfg.Memory != "" {
			_, err := bytesize.Parse(cfg.Memory)
			if err != nil {
				return fmt.Errorf("invalid memory setting (%s): %w", cfg.Memory, err)
			}
		}

		if cfg.Swap != "" {
			_, err := bytesize.Parse(cfg.Swap)
			if err != nil {
				return fmt.Errorf("invalid swap setting (%s): %w", cfg.Swap, err)
			}
		}

		_, err := bytesize.Parse(cfg.DataSize)
		if err != nil {
			return fmt.Errorf("invalid data size setting (%s): %w", cfg.DataSize, err)
		}

		_, err = bytesize.Parse(cfg.UserSize)
		if err != nil {
			return fmt.Errorf("invalid data size setting (%s): %w", cfg.UserSize, err)
		}

		var rewrite bool

		if cfg.ClusterId == "" {
			cfg.ClusterId = newUniqueId()
			rewrite = true
		}

		if rewrite {
			of, err := os.Create(configPath)
			if err != nil {
				return err
			}

			defer of.Close()

			err = json.NewEncoder(of).Encode(&cfg)
			if err != nil {
				return err
			}
		}
	}

	return nil
}
