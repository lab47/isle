package vm

type Config struct {
	Cores      int    `json:"cores"`
	Memory     string `json:"ram"`
	Swap       string `json:"swap"`
	DataSize   string `json:"system_disk"`
	UserSize   string `json:"user_disk"`
	MacAddress string `json:"mac_address"`
}
