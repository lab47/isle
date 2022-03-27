package types

const ProtocolByte = 0x7

type ControlMessage struct {
	HeaderMessage
}

const (
	Unknown = 0
	OK      = 1
	Error   = 2
)

type ResponseMessage struct {
	Code  int
	Error string
}

type HeaderMessage struct {
	Kind string
}

type PortForwardMessage struct {
	Port int
	Key  string
}

type RunningMessage struct {
	IP string
}

type ShellMessage struct {
	Command []string
	Rows    uint16
	Cols    uint16
}

type StatusMessage struct {
	Error string
}

type MSLInfo struct {
	Name     string `json:"name"`
	Image    string `json:"image"`
	Dir      string `json:"dir"`
	AsRoot   bool   `json:"as_root"`
	Hostname string `json:"hostname"`
	UserName string `json:"user_name"`
	UserId   int    `json:"user_id"`
}
