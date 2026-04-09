package config

type Config struct {
	ListenAddress string        `json:"listen_address"`
	Auth          AuthConfig    `json:"auth"`
	Mounts        []MountConfig `json:"mounts"`
	Timeouts      Timeouts      `json:"timeouts"`
	Logging       Logging       `json:"logging"`
}

type AuthConfig struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Realm    string `json:"realm"`
}

type MountConfig struct {
	Name           string `json:"name"`
	Host           string `json:"host"`
	Port           int    `json:"port"`
	Username       string `json:"username"`
	Password       string `json:"password"`
	RootPath       string `json:"root_path"`
	TLSMode        string `json:"tls_mode"`
	PassiveMode    *bool  `json:"passive_mode"`
	ConnectTimeout int    `json:"connect_timeout"`
	ConnectionPool int    `json:"connection_pool_size"`
}

type Timeouts struct {
	ConnectSeconds int `json:"connect_seconds"`
	ReadSeconds    int `json:"read_seconds"`
}

type Logging struct {
	Level string `json:"level"`
}
