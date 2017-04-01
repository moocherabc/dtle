package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

// This is the default port that we use for Serf communication
const (
	DefaultBindPort  int = 8191
	DefaultClusterID     = "udup-cluster"
)

// Config is the configuration for the Udup agent.
type Config struct {
	LogLevel string `mapstructure:"log_level"`
	// Region is the region this agent is in. Defaults to global.
	Region string
	// Datacenter is the datacenter this agent is in. Defaults to dc1
	Datacenter string
	// NodeName is the name we register as. Defaults to hostname.
	NodeName string `mapstructure:"name"`
	// BindAddr is the address on which all of nomad's services will
	// be bound. If not specified, this defaults to 127.0.0.1.
	BindAddr              string `mapstructure:"bind_addr"`
	AdvertiseAddr         string `mapstructure:"advertise"`
	Interface             string
	ReconnectInterval     time.Duration `mapstructure:"reconnect_interval"`
	ReconnectTimeout      time.Duration `mapstructure:"reconnect_timeout"`
	TombstoneTimeout      time.Duration `mapstructure:"tombstone_timeout"`
	DisableNameResolution bool
	RejoinAfterLeave      bool `mapstructure:"rejoin"`
	// Client has our client related settings
	Client *ClientConfig `mapstructure:"client"`

	// Server has our server related settings
	Server *ServerConfig `mapstructure:"server"`

	RPCPort int `mapstructure:"rpc_port"`
	Version string

	Nats   *NatsConfig   `mapstructure:"nats"`
	Consul *ConsulConfig `mapstructure:"consul"`

	// config file that have been loaded (in order)
	PidFile string `mapstructure:"pid_file"`
	File    string `mapstructure:"-"`
}

type ServerConfig struct {
	// Enabled controls if we are a server
	Enabled  bool   `mapstructure:"enabled"`
	HTTPAddr string `mapstructure:"http_addr"`
}

type ClientConfig struct {
	// StartJoin is a list of addresses to attempt to join when the
	// agent starts. If Serf is unable to communicate with any of these
	// addresses, then the agent will error and exit.
	Join []string `mapstructure:"join"`
}

type NatsConfig struct {
	Addr         string `mapstructure:"nats_addr"`
	StoreType    string `mapstructure:"nats_store_type"`
	FilestoreDir string `mapstructure:"nats_file_store_dir"`
}

type ConsulConfig struct {
	Addrs          []string `mapstructure:"addrs"`
	ServerAutoJoin bool     `mapstructure:"server_auto_join"`
	ClientAutoJoin bool     `mapstructure:"client_auto_join"`
}

// DriverConfig is the DB configuration.
type DriverConfig struct {
	// Node name of the node that run this job.
	NodeName string `json:"node_name,omitempty"`
	Running  bool   `json:"running"`
	//Ref:http://dev.mysql.com/doc/refman/5.7/en/replication-options-slave.html#option_mysqld_replicate-do-table
	ReplicateDoTable []TableName       `json:"replicate_do_table"`
	ReplicateDoDb    []string          `json:"replicate_do_db"`
	MaxRetries       int64             `json:"max_retries"`
	Gtid             string            `json:"gtid"`
	Driver           string            `json:"driver"`
	ServerID         uint32            `json:"server_id"`
	NatsAddr         string            `json:"nats_addr"`
	WorkerCount      int               `json:"worker_count"`
	ConnCfg          *ConnectionConfig `json:"conn_cfg"`
	ErrCh            chan error        `json:"-"`
	GtidCh           chan string       `json:"-"`
}

// ConnectionConfig is the DB configuration.
type ConnectionConfig struct {
	Host string `json:"host"`

	User string `json:"user"`

	Password string `json:"password"`

	Port int `json:"port"`
}

func (c *ConnectionConfig) String() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

// TableName is the table configuration
// slave restrict replication to a given table
type TableName struct {
	Schema string `mapstructure:"db_name"`
	Name   string `mapstructure:"tbl_name"`
}

// DefaultConfig is a the baseline configuration for Udup
func DefaultConfig() *Config {
	hostname, err := os.Hostname()
	if err != nil {
		panic(err)
	}
	return &Config{
		NodeName:   hostname,
		File:       "udup.conf",
		LogLevel:   "info",
		Region:     "global",
		Datacenter: "dc1",
		BindAddr:   "0.0.0.0",
		PidFile:    "udup.pid",
		Consul:     DefaultConsulConfig(),
		Client: &ClientConfig{
			Join: []string{},
		},
		Server: &ServerConfig{
			Enabled: false,
		},
	}
}

// DefaultConsulConfig() returns the canonical defaults for the Nomad
// `consul` configuration.
func DefaultConsulConfig() *ConsulConfig {
	return &ConsulConfig{
		ServerAutoJoin: true,
		ClientAutoJoin: true,
		//Timeout:           5 * time.Second,
	}
}

// Listener can be used to get a new listener using a custom bind address.
// If the bind provided address is empty, the BindAddr is used instead.
func (c *Config) Listener(proto, addr string, port int) (net.Listener, error) {
	if addr == "" {
		addr = c.BindAddr
	}

	// Do our own range check to avoid bugs in package net.
	//
	//   golang.org/issue/11715
	//   golang.org/issue/13447
	//
	// Both of the above bugs were fixed by golang.org/cl/12447 which will be
	// included in Go 1.6. The error returned below is the same as what Go 1.6
	// will return.
	if 0 > port || port > 65535 {
		return nil, &net.OpError{
			Op:  "listen",
			Net: proto,
			Err: &net.AddrError{Err: "invalid port", Addr: fmt.Sprint(port)},
		}
	}
	return net.Listen(proto, fmt.Sprintf("%s:%d", addr, port))
}

// Merge merges two configurations.
func (c *Config) Merge(b *Config) *Config {
	result := *c

	if b.NodeName != "" {
		result.NodeName = b.NodeName
	}

	if b.LogLevel != "" {
		result.LogLevel = b.LogLevel
	}

	if b.Region != "" {
		result.Region = b.Region
	}

	if b.Datacenter != "" {
		result.Datacenter = b.Datacenter
	}

	if b.BindAddr != "" {
		result.BindAddr = b.BindAddr
	}

	if b.AdvertiseAddr != "" {
		result.AdvertiseAddr = b.AdvertiseAddr
	}

	if b.Interface != "" {
		result.Interface = b.Interface
	}

	if b.RPCPort != 0 {
		result.RPCPort = b.RPCPort
	}

	// Apply the client config
	if result.Client == nil && b.Client != nil {
		client := *b.Client
		result.Client = &client
	} else if b.Client != nil {
		result.Client = result.Client.Merge(b.Client)
	}

	// Apply the server config
	if result.Server == nil && b.Server != nil {
		server := *b.Server
		result.Server = &server
	} else if b.Server != nil {
		result.Server = result.Server.Merge(b.Server)
	}

	// Apply the client config
	if result.Nats == nil && b.Nats != nil {
		nats := *b.Nats
		result.Nats = &nats
	} else if b.Nats != nil {
		result.Nats = result.Nats.Merge(b.Nats)
	}

	if result.Consul == nil && b.Consul != nil {
		consul := *b.Consul
		result.Consul = &consul
	} else if b.Consul != nil {
		result.Consul = result.Consul.Merge(b.Consul)
	}

	if b.PidFile != "" {
		result.PidFile = b.PidFile
	}

	if b.File != "" {
		result.File = b.File
	}

	return &result
}

// Merge is used to merge two server configs together
func (a *ServerConfig) Merge(b *ServerConfig) *ServerConfig {
	result := *a

	if b.Enabled {
		result.Enabled = true
	}

	if b.HTTPAddr != "" {
		result.HTTPAddr = b.HTTPAddr
	}

	return &result
}

// Merge is used to merge two client configs together
func (a *ClientConfig) Merge(b *ClientConfig) *ClientConfig {
	result := *a

	// Copy the start join addresses
	result.Join = make([]string, 0, len(a.Join)+len(b.Join))
	result.Join = append(result.Join, a.Join...)
	result.Join = append(result.Join, b.Join...)
	return &result
}

// Merge is used to merge two Nats configs together
func (a *NatsConfig) Merge(b *NatsConfig) *NatsConfig {
	result := *a

	if b.Addr != "" {
		result.Addr = b.Addr
	}

	if b.StoreType != "" {
		result.StoreType = b.StoreType
	}

	if b.FilestoreDir != "" {
		result.FilestoreDir = b.FilestoreDir
	}

	return &result
}

// Merge merges two Consul configurations together.
func (a *ConsulConfig) Merge(b *ConsulConfig) *ConsulConfig {
	result := *a

	result.Addrs = append(result.Addrs, b.Addrs...)
	return &result
}

// LoadConfig loads the configuration at the given path, regardless if
// its a file or directory.
func LoadConfig(path string) (*Config, error) {
	cleaned := filepath.Clean(path)
	config, err := ParseConfigFile(cleaned)
	if err != nil {
		return nil, err
	}

	config.File = cleaned
	return config, nil
}

// AddrParts returns the parts of the BindAddr that should be
// used to configure Serf.
func (c *Config) AddrParts(address string) (string, int, error) {
	checkAddr := address

START:
	_, _, err := net.SplitHostPort(checkAddr)
	if ae, ok := err.(*net.AddrError); ok && ae.Err == "missing port in address" {
		checkAddr = fmt.Sprintf("%s:%d", checkAddr, DefaultBindPort)
		goto START
	}
	if err != nil {
		return "", 0, err
	}

	// Get the address
	addr, err := net.ResolveTCPAddr("tcp", checkAddr)
	if err != nil {
		return "", 0, err
	}

	return addr.IP.String(), addr.Port, nil
}

// Networkinterface is used to get the associated network
// interface from the configured value
func (c *Config) NetworkInterface() (*net.Interface, error) {
	if c.Interface == "" {
		return nil, nil
	}
	return net.InterfaceByName(c.Interface)
}
