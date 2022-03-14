package vm

type Config struct {
	Cores      int    `json:"cores"`
	Memory     int    `json:"ram_in_gigabytes"`
	DataSize   int    `json:"system_disk_in_gigabytes"`
	UserSize   int    `json:"user_disk_in_gigabytes"`
	MacAddress string `json:"mac_address"`
}
