// Copyright 2012-2018 The NATS Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package server

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/gnatsd/conf"
	"github.com/nats-io/nkeys"
)

// ClusterOpts are options for clusters.
type ClusterOpts struct {
	Host           string            `json:"addr,omitempty"`
	Port           int               `json:"cluster_port,omitempty"`
	Username       string            `json:"-"`
	Password       string            `json:"-"`
	AuthTimeout    float64           `json:"auth_timeout,omitempty"`
	Permissions    *RoutePermissions `json:"-"`
	TLSTimeout     float64           `json:"-"`
	TLSConfig      *tls.Config       `json:"-"`
	ListenStr      string            `json:"-"`
	Advertise      string            `json:"-"`
	NoAdvertise    bool              `json:"-"`
	ConnectRetries int               `json:"-"`
}

// Options block for gnatsd server.
type Options struct {
	ConfigFile       string        `json:"-"`
	Host             string        `json:"addr"`
	Port             int           `json:"port"`
	ClientAdvertise  string        `json:"-"`
	Trace            bool          `json:"-"`
	Debug            bool          `json:"-"`
	NoLog            bool          `json:"-"`
	NoSigs           bool          `json:"-"`
	Logtime          bool          `json:"-"`
	MaxConn          int           `json:"max_connections"`
	MaxSubs          int           `json:"max_subscriptions,omitempty"`
	Nkeys            []*NkeyUser   `json:"-"`
	Users            []*User       `json:"-"`
	Accounts         []*Account    `json:"-"`
	AllowNewAccounts bool          `json:"-"`
	Username         string        `json:"-"`
	Password         string        `json:"-"`
	Authorization    string        `json:"-"`
	PingInterval     time.Duration `json:"ping_interval"`
	MaxPingsOut      int           `json:"ping_max"`
	HTTPHost         string        `json:"http_host"`
	HTTPPort         int           `json:"http_port"`
	HTTPSPort        int           `json:"https_port"`
	AuthTimeout      float64       `json:"auth_timeout"`
	MaxControlLine   int           `json:"max_control_line"`
	MaxPayload       int           `json:"max_payload"`
	MaxPending       int64         `json:"max_pending"`
	Cluster          ClusterOpts   `json:"cluster,omitempty"`
	ProfPort         int           `json:"-"`
	PidFile          string        `json:"-"`
	PortsFileDir     string        `json:"-"`
	LogFile          string        `json:"-"`
	Syslog           bool          `json:"-"`
	RemoteSyslog     string        `json:"-"`
	Routes           []*url.URL    `json:"-"`
	RoutesStr        string        `json:"-"`
	TLSTimeout       float64       `json:"tls_timeout"`
	TLS              bool          `json:"-"`
	TLSVerify        bool          `json:"-"`
	TLSCert          string        `json:"-"`
	TLSKey           string        `json:"-"`
	TLSCaCert        string        `json:"-"`
	TLSConfig        *tls.Config   `json:"-"`
	WriteDeadline    time.Duration `json:"-"`
	RQSubsSweep      time.Duration `json:"-"` // Deprecated
	MaxClosedClients int           `json:"-"`
	LameDuckDuration time.Duration `json:"-"`

	CustomClientAuthentication Authentication `json:"-"`
	CustomRouterAuthentication Authentication `json:"-"`

	// CheckConfig configuration file syntax test was successful and exit.
	CheckConfig bool `json:"-"`
}

// Clone performs a deep copy of the Options struct, returning a new clone
// with all values copied.
func (o *Options) Clone() *Options {
	if o == nil {
		return nil
	}
	clone := &Options{}
	*clone = *o
	if o.Users != nil {
		clone.Users = make([]*User, len(o.Users))
		for i, user := range o.Users {
			clone.Users[i] = user.clone()
		}
	}
	if o.Nkeys != nil {
		clone.Nkeys = make([]*NkeyUser, len(o.Nkeys))
		for i, nkey := range o.Nkeys {
			clone.Nkeys[i] = nkey.clone()
		}
	}

	if o.Routes != nil {
		clone.Routes = make([]*url.URL, len(o.Routes))
		for i, route := range o.Routes {
			routeCopy := &url.URL{}
			*routeCopy = *route
			clone.Routes[i] = routeCopy
		}
	}
	if o.TLSConfig != nil {
		clone.TLSConfig = o.TLSConfig.Clone()
	}
	if o.Cluster.TLSConfig != nil {
		clone.Cluster.TLSConfig = o.Cluster.TLSConfig.Clone()
	}
	return clone
}

// Configuration file authorization section.
type authorization struct {
	// Singles
	user  string
	pass  string
	token string
	// Multiple Nkeys/Users
	nkeys              []*NkeyUser
	users              []*User
	timeout            float64
	defaultPermissions *Permissions
}

// TLSConfigOpts holds the parsed tls config information,
// used with flag parsing
type TLSConfigOpts struct {
	CertFile         string
	KeyFile          string
	CaFile           string
	Verify           bool
	Timeout          float64
	Ciphers          []uint16
	CurvePreferences []tls.CurveID
}

var tlsUsage = `
TLS configuration is specified in the tls section of a configuration file:

e.g.

    tls {
        cert_file: "./certs/server-cert.pem"
        key_file:  "./certs/server-key.pem"
        ca_file:   "./certs/ca.pem"
        verify:    true

        cipher_suites: [
            "TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256",
            "TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256"
        ]
        curve_preferences: [
            "CurveP256",
            "CurveP384",
            "CurveP521"
        ]
    }

Available cipher suites include:
`

// ProcessConfigFile processes a configuration file.
// FIXME(dlc): A bit hacky
func ProcessConfigFile(configFile string) (*Options, error) {
	opts := &Options{}
	if err := opts.ProcessConfigFile(configFile); err != nil {
		// If only warnings then continue and return the options.
		if cerr, ok := err.(*processConfigErr); ok && len(cerr.Errors()) == 0 {
			return opts, nil
		}

		return nil, err
	}
	return opts, nil
}

// token is an item parsed from the configuration.
type token interface {
	Value() interface{}
	Line() int
	IsUsedVariable() bool
	SourceFile() string
	Position() int
}

// unwrapValue can be used to get the token and value from an item
// to be able to report the line number in case of an incorrect
// configuration.
func unwrapValue(v interface{}) (token, interface{}) {
	switch tk := v.(type) {
	case token:
		return tk, tk.Value()
	default:
		return nil, v
	}
}

// ProcessConfigFile updates the Options structure with options
// present in the given configuration file.
// This version is convenient if one wants to set some default
// options and then override them with what is in the config file.
// For instance, this version allows you to do something such as:
//
// opts := &Options{Debug: true}
// opts.ProcessConfigFile(myConfigFile)
//
// If the config file contains "debug: false", after this call,
// opts.Debug would really be false. It would be impossible to
// achieve that with the non receiver ProcessConfigFile() version,
// since one would not know after the call if "debug" was not present
// or was present but set to false.
func (o *Options) ProcessConfigFile(configFile string) error {
	o.ConfigFile = configFile
	if configFile == "" {
		return nil
	}
	m, err := conf.ParseFileWithChecks(configFile)
	if err != nil {
		return err
	}
	// Collect all errors and warnings and report them all together.
	errors := make([]error, 0)
	warnings := make([]error, 0)

	for k, v := range m {
		tk, v := unwrapValue(v)
		switch strings.ToLower(k) {
		case "listen":
			hp, err := parseListen(v)
			if err != nil {
				errors = append(errors, &configErr{tk, err.Error()})
				continue
			}
			o.Host = hp.host
			o.Port = hp.port
		case "client_advertise":
			o.ClientAdvertise = v.(string)
		case "port":
			o.Port = int(v.(int64))
		case "host", "net":
			o.Host = v.(string)
		case "debug":
			o.Debug = v.(bool)
		case "trace":
			o.Trace = v.(bool)
		case "logtime":
			o.Logtime = v.(bool)
		case "accounts":
			err := parseAccounts(tk, o, &errors, &warnings)
			if err != nil {
				errors = append(errors, err)
				continue
			}
		case "authorization":
			auth, err := parseAuthorization(tk, o, &errors, &warnings)
			if err != nil {
				errors = append(errors, err)
				continue
			}

			o.Username = auth.user
			o.Password = auth.pass
			o.Authorization = auth.token
			if (auth.user != "" || auth.pass != "") && auth.token != "" {
				err := &configErr{tk, fmt.Sprintf("Cannot have a user/pass and token")}
				errors = append(errors, err)
				continue
			}
			o.AuthTimeout = auth.timeout
			// Check for multiple users defined
			if auth.users != nil {
				if auth.user != "" {
					err := &configErr{tk, fmt.Sprintf("Can not have a single user/pass and a users array")}
					errors = append(errors, err)
					continue
				}
				if auth.token != "" {
					err := &configErr{tk, fmt.Sprintf("Can not have a token and a users array")}
					errors = append(errors, err)
					continue
				}
				// Users may have been added from Accounts parsing, so do an append here
				o.Users = append(o.Users, auth.users...)
			}

			// Check for nkeys
			if auth.nkeys != nil {
				// NKeys may have been added from Accounts parsing, so do an append here
				o.Nkeys = append(o.Nkeys, auth.nkeys...)
			}
		case "http":
			hp, err := parseListen(v)
			if err != nil {
				err := &configErr{tk, err.Error()}
				errors = append(errors, err)
				continue
			}
			o.HTTPHost = hp.host
			o.HTTPPort = hp.port
		case "https":
			hp, err := parseListen(v)
			if err != nil {
				err := &configErr{tk, err.Error()}
				errors = append(errors, err)
				continue
			}
			o.HTTPHost = hp.host
			o.HTTPSPort = hp.port
		case "http_port", "monitor_port":
			o.HTTPPort = int(v.(int64))
		case "https_port":
			o.HTTPSPort = int(v.(int64))
		case "cluster":
			err := parseCluster(tk, o, &errors, &warnings)
			if err != nil {
				errors = append(errors, err)
				continue
			}
		case "logfile", "log_file":
			o.LogFile = v.(string)
		case "syslog":
			o.Syslog = v.(bool)
		case "remote_syslog":
			o.RemoteSyslog = v.(string)
		case "pidfile", "pid_file":
			o.PidFile = v.(string)
		case "ports_file_dir":
			o.PortsFileDir = v.(string)
		case "prof_port":
			o.ProfPort = int(v.(int64))
		case "max_control_line":
			o.MaxControlLine = int(v.(int64))
		case "max_payload":
			o.MaxPayload = int(v.(int64))
		case "max_pending":
			o.MaxPending = v.(int64)
		case "max_connections", "max_conn":
			o.MaxConn = int(v.(int64))
		case "max_subscriptions", "max_subs":
			o.MaxSubs = int(v.(int64))
		case "ping_interval":
			o.PingInterval = time.Duration(int(v.(int64))) * time.Second
		case "ping_max":
			o.MaxPingsOut = int(v.(int64))
		case "tls":
			tc, err := parseTLS(tk, o)
			if err != nil {
				errors = append(errors, err)
				continue
			}
			if o.TLSConfig, err = GenTLSConfig(tc); err != nil {
				err := &configErr{tk, err.Error()}
				errors = append(errors, err)
				continue
			}
			o.TLSTimeout = tc.Timeout
		case "write_deadline":
			wd, ok := v.(string)
			if ok {
				dur, err := time.ParseDuration(wd)
				if err != nil {
					err := &configErr{tk, fmt.Sprintf("error parsing write_deadline: %v", err)}
					errors = append(errors, err)
					continue
				}
				o.WriteDeadline = dur
			} else {
				// Backward compatible with old type, assume this is the
				// number of seconds.
				o.WriteDeadline = time.Duration(v.(int64)) * time.Second
				err := &configWarningErr{
					field: k,
					configErr: configErr{
						token:  tk,
						reason: "write_deadline should be converted to a duration",
					},
				}
				warnings = append(warnings, err)
			}
		case "lame_duck_duration":
			dur, err := time.ParseDuration(v.(string))
			if err != nil {
				err := &configErr{tk, fmt.Sprintf("error parsing lame_duck_duration: %v", err)}
				errors = append(errors, err)
				continue
			}
			o.LameDuckDuration = dur
		default:
			if !tk.IsUsedVariable() {
				err := &unknownConfigFieldErr{
					field: k,
					configErr: configErr{
						token: tk,
					},
				}
				errors = append(errors, err)
			}
		}
	}

	if len(errors) > 0 || len(warnings) > 0 {
		return &processConfigErr{
			errors:   errors,
			warnings: warnings,
		}
	}

	return nil
}

// hostPort is simple struct to hold parsed listen/addr strings.
type hostPort struct {
	host string
	port int
}

// parseListen will parse listen option which is replacing host/net and port
func parseListen(v interface{}) (*hostPort, error) {
	hp := &hostPort{}
	switch vv := v.(type) {
	// Only a port
	case int64:
		hp.port = int(vv)
	case string:
		host, port, err := net.SplitHostPort(vv)
		if err != nil {
			return nil, fmt.Errorf("Could not parse address string %q", vv)
		}
		hp.port, err = strconv.Atoi(port)
		if err != nil {
			return nil, fmt.Errorf("Could not parse port %q", port)
		}
		hp.host = host
	}
	return hp, nil
}

// parseCluster will parse the cluster config.
func parseCluster(v interface{}, opts *Options, errors *[]error, warnings *[]error) error {
	tk, v := unwrapValue(v)
	cm, ok := v.(map[string]interface{})
	if !ok {
		return &configErr{tk, fmt.Sprintf("Expected map to define cluster, got %T", v)}
	}

	for mk, mv := range cm {
		// Again, unwrap token value if line check is required.
		tk, mv = unwrapValue(mv)
		switch strings.ToLower(mk) {
		case "listen":
			hp, err := parseListen(mv)
			if err != nil {
				err := &configErr{tk, err.Error()}
				*errors = append(*errors, err)
				continue
			}
			opts.Cluster.Host = hp.host
			opts.Cluster.Port = hp.port
		case "port":
			opts.Cluster.Port = int(mv.(int64))
		case "host", "net":
			opts.Cluster.Host = mv.(string)
		case "authorization":
			auth, err := parseAuthorization(tk, opts, errors, warnings)
			if err != nil {
				*errors = append(*errors, err)
				continue
			}
			if auth.users != nil {
				err := &configErr{tk, fmt.Sprintf("Cluster authorization does not allow multiple users")}
				*errors = append(*errors, err)
				continue
			}
			opts.Cluster.Username = auth.user
			opts.Cluster.Password = auth.pass
			opts.Cluster.AuthTimeout = auth.timeout

			if auth.defaultPermissions != nil {
				err := &configWarningErr{
					field: mk,
					configErr: configErr{
						token:  tk,
						reason: `setting "permissions" within cluster authorization block is deprecated`,
					},
				}
				*warnings = append(*warnings, err)

				// Do not set permissions if they were specified in top-level cluster block.
				if opts.Cluster.Permissions == nil {
					setClusterPermissions(&opts.Cluster, auth.defaultPermissions)
				}
			}
		case "routes":
			ra := mv.([]interface{})
			opts.Routes = make([]*url.URL, 0, len(ra))
			for _, r := range ra {
				tk, r := unwrapValue(r)
				routeURL := r.(string)
				url, err := url.Parse(routeURL)
				if err != nil {
					err := &configErr{tk, fmt.Sprintf("error parsing route url [%q]", routeURL)}
					*errors = append(*errors, err)
					continue
				}
				opts.Routes = append(opts.Routes, url)
			}
		case "tls":
			tc, err := parseTLS(tk, opts)
			if err != nil {
				*errors = append(*errors, err)
				continue
			}
			if opts.Cluster.TLSConfig, err = GenTLSConfig(tc); err != nil {
				err := &configErr{tk, err.Error()}
				*errors = append(*errors, err)
				continue
			}
			// For clusters, we will force strict verification. We also act
			// as both client and server, so will mirror the rootCA to the
			// clientCA pool.
			opts.Cluster.TLSConfig.ClientAuth = tls.RequireAndVerifyClientCert
			opts.Cluster.TLSConfig.RootCAs = opts.Cluster.TLSConfig.ClientCAs
			opts.Cluster.TLSTimeout = tc.Timeout
		case "cluster_advertise", "advertise":
			opts.Cluster.Advertise = mv.(string)
		case "no_advertise":
			opts.Cluster.NoAdvertise = mv.(bool)
		case "connect_retries":
			opts.Cluster.ConnectRetries = int(mv.(int64))
		case "permissions":
			perms, err := parseUserPermissions(mv, opts, errors, warnings)
			if err != nil {
				*errors = append(*errors, err)
				continue
			}
			// This will possibly override permissions that were define in auth block
			setClusterPermissions(&opts.Cluster, perms)
		default:
			if !tk.IsUsedVariable() {
				err := &unknownConfigFieldErr{
					field: mk,
					configErr: configErr{
						token: tk,
					},
				}
				*errors = append(*errors, err)
				continue
			}
		}
	}
	return nil
}

// Sets cluster's permissions based on given pub/sub permissions,
// doing the appropriate translation.
func setClusterPermissions(opts *ClusterOpts, perms *Permissions) {
	// Import is whether or not we will send a SUB for interest to the other side.
	// Export is whether or not we will accept a SUB from the remote for a given subject.
	// Both only effect interest registration.
	// The parsing sets Import into Publish and Export into Subscribe, convert
	// accordingly.
	opts.Permissions = &RoutePermissions{
		Import: perms.Publish,
		Export: perms.Subscribe,
	}
}

// Temp structures to hold account import and export defintions since they need
// to be processed after being parsed.
type export struct {
	acc  *Account
	sub  string
	accs []string
}

type importStream struct {
	acc *Account
	an  string
	sub string
	pre string
}

type importService struct {
	acc *Account
	an  string
	sub string
	to  string
}

// Checks if an account name is reserved.
func isReservedAccount(name string) bool {
	return name == globalAccountName
}

// parseAccounts will parse the different accounts syntax.
func parseAccounts(v interface{}, opts *Options, errors *[]error, warnings *[]error) error {
	var (
		importStreams  []*importStream
		importServices []*importService
		exportStreams  []*export
		exportServices []*export
	)
	tk, v := unwrapValue(v)
	switch vv := v.(type) {
	// Simple array of account names.
	case []interface{}, []string:
		m := make(map[string]struct{}, len(v.([]interface{})))
		for _, n := range v.([]interface{}) {
			tk, name := unwrapValue(n)
			ns := name.(string)
			// Check for reserved names.
			if isReservedAccount(ns) {
				err := &configErr{tk, fmt.Sprintf("%q is a Reserved Account", ns)}
				*errors = append(*errors, err)
				continue
			}
			if _, ok := m[ns]; ok {
				err := &configErr{tk, fmt.Sprintf("Duplicate Account Entry: %s", ns)}
				*errors = append(*errors, err)
				continue
			}
			opts.Accounts = append(opts.Accounts, &Account{Name: ns})
			m[ns] = struct{}{}
		}
	// More common map entry
	case map[string]interface{}:
		// Track users across accounts, must be unique across
		// accounts and nkeys vs users.
		uorn := make(map[string]struct{})
		for aname, mv := range vv {
			tk, amv := unwrapValue(mv)

			// Skip referenced config vars within the account block.
			if tk.IsUsedVariable() {
				continue
			}

			// These should be maps.
			mv, ok := amv.(map[string]interface{})
			if !ok {
				err := &configErr{tk, "Expected map entries for accounts"}
				*errors = append(*errors, err)
				continue
			}
			if isReservedAccount(aname) {
				err := &configErr{tk, fmt.Sprintf("%q is a Reserved Account", aname)}
				*errors = append(*errors, err)
				continue
			}
			acc := &Account{Name: aname}
			opts.Accounts = append(opts.Accounts, acc)

			for k, v := range mv {
				tk, mv := unwrapValue(v)
				switch strings.ToLower(k) {
				case "nkey":
					nk, ok := mv.(string)
					if !ok || !nkeys.IsValidPublicAccountKey(nk) {
						err := &configErr{tk, fmt.Sprintf("Not a valid public nkey for an account: %q", mv)}
						*errors = append(*errors, err)
						continue
					}
					acc.Nkey = nk
				case "imports":
					streams, services, err := parseAccountImports(tk, acc, errors, warnings)
					if err != nil {
						*errors = append(*errors, err)
						continue
					}
					importStreams = append(importStreams, streams...)
					importServices = append(importServices, services...)
				case "exports":
					streams, services, err := parseAccountExports(tk, acc, errors, warnings)
					if err != nil {
						*errors = append(*errors, err)
						continue
					}
					exportStreams = append(exportStreams, streams...)
					exportServices = append(exportServices, services...)
				case "users":
					nkeys, users, err := parseUsers(mv, opts, errors, warnings)
					if err != nil {
						*errors = append(*errors, err)
						continue
					}
					for _, u := range users {
						if _, ok := uorn[u.Username]; ok {
							err := &configErr{tk, fmt.Sprintf("Duplicate user %q detected", u.Username)}
							*errors = append(*errors, err)
							continue
						}
						uorn[u.Username] = struct{}{}
						u.Account = acc
					}
					opts.Users = append(opts.Users, users...)

					for _, u := range nkeys {
						if _, ok := uorn[u.Nkey]; ok {
							err := &configErr{tk, fmt.Sprintf("Duplicate nkey %q detected", u.Nkey)}
							*errors = append(*errors, err)
							continue
						}
						uorn[u.Nkey] = struct{}{}
						u.Account = acc
					}
					opts.Nkeys = append(opts.Nkeys, nkeys...)
				default:
					if !tk.IsUsedVariable() {
						err := &unknownConfigFieldErr{
							field: k,
							configErr: configErr{
								token: tk,
							},
						}
						*errors = append(*errors, err)
					}
				}
			}
		}
	}
	// Bail already if there are previous errors.
	if len(*errors) > 0 {
		return nil
	}

	// Parse Imports and Exports here after all accounts defined.
	// Do exports first since they need to be defined for imports to succeed
	// since we do permissions checks.

	// Create a lookup map for accounts lookups.
	am := make(map[string]*Account, len(opts.Accounts))
	for _, a := range opts.Accounts {
		am[a.Name] = a
	}
	// Do stream exports
	for _, stream := range exportStreams {
		// Make array of accounts if applicable.
		var accounts []*Account
		for _, an := range stream.accs {
			ta := am[an]
			if ta == nil {
				msg := fmt.Sprintf("%q account not defined for stream export", an)
				*errors = append(*errors, &configErr{tk, msg})
				continue
			}
			accounts = append(accounts, ta)
		}
		if err := stream.acc.AddStreamExport(stream.sub, accounts); err != nil {
			msg := fmt.Sprintf("Error adding stream export %q: %v", stream.sub, err)
			*errors = append(*errors, &configErr{tk, msg})
			continue
		}
	}
	for _, service := range exportServices {
		// Make array of accounts if applicable.
		var accounts []*Account
		for _, an := range service.accs {
			ta := am[an]
			if ta == nil {
				msg := fmt.Sprintf("%q account not defined for service export", an)
				*errors = append(*errors, &configErr{tk, msg})
				continue
			}
			accounts = append(accounts, ta)
		}
		if err := service.acc.AddServiceExport(service.sub, accounts); err != nil {
			msg := fmt.Sprintf("Error adding service export %q: %v", service.sub, err)
			*errors = append(*errors, &configErr{tk, msg})
			continue
		}
	}
	for _, stream := range importStreams {
		ta := am[stream.an]
		if ta == nil {
			msg := fmt.Sprintf("%q account not defined for stream import", stream.an)
			*errors = append(*errors, &configErr{tk, msg})
			continue
		}
		if err := stream.acc.AddStreamImport(ta, stream.sub, stream.pre); err != nil {
			msg := fmt.Sprintf("Error adding stream import %q: %v", stream.sub, err)
			*errors = append(*errors, &configErr{tk, msg})
			continue
		}
	}
	for _, service := range importServices {
		ta := am[service.an]
		if ta == nil {
			msg := fmt.Sprintf("%q account not defined for service import", service.an)
			*errors = append(*errors, &configErr{tk, msg})
			continue
		}
		if service.to == "" {
			service.to = service.sub
		}
		if err := service.acc.AddServiceImport(ta, service.to, service.sub); err != nil {
			msg := fmt.Sprintf("Error adding service import %q: %v", service.sub, err)
			*errors = append(*errors, &configErr{tk, msg})
			continue
		}
	}

	return nil
}

// Parse the account imports
func parseAccountExports(v interface{}, acc *Account, errors, warnings *[]error) ([]*export, []*export, error) {
	// This should be an array of objects/maps.
	tk, v := unwrapValue(v)
	ims, ok := v.([]interface{})
	if !ok {
		return nil, nil, &configErr{tk, fmt.Sprintf("Exports should be an array, got %T", v)}
	}

	var services []*export
	var streams []*export

	for _, v := range ims {
		// Should have stream or service
		stream, service, err := parseExportStreamOrService(v, errors, warnings)
		if err != nil {
			*errors = append(*errors, err)
			continue
		}
		if service != nil {
			service.acc = acc
			services = append(services, service)
		}
		if stream != nil {
			stream.acc = acc
			streams = append(streams, stream)
		}
	}
	return streams, services, nil
}

// Parse the account imports
func parseAccountImports(v interface{}, acc *Account, errors, warnings *[]error) ([]*importStream, []*importService, error) {
	// This should be an array of objects/maps.
	tk, v := unwrapValue(v)
	ims, ok := v.([]interface{})
	if !ok {
		return nil, nil, &configErr{tk, fmt.Sprintf("Imports should be an array, got %T", v)}
	}

	var services []*importService
	var streams []*importStream

	for _, v := range ims {
		// Should have stream or service
		stream, service, err := parseImportStreamOrService(v, errors, warnings)
		if err != nil {
			*errors = append(*errors, err)
			continue
		}
		if service != nil {
			service.acc = acc
			services = append(services, service)
		}
		if stream != nil {
			stream.acc = acc
			streams = append(streams, stream)
		}
	}
	return streams, services, nil
}

// Helper to parse an embedded account description for imported services or streams.
func parseAccount(v map[string]interface{}, errors, warnings *[]error) (string, string, error) {
	var accountName, subject string
	for mk, mv := range v {
		tk, mv := unwrapValue(mv)
		switch strings.ToLower(mk) {
		case "account":
			accountName = mv.(string)
		case "subject":
			subject = mv.(string)
		default:
			if !tk.IsUsedVariable() {
				err := &unknownConfigFieldErr{
					field: mk,
					configErr: configErr{
						token: tk,
					},
				}
				*errors = append(*errors, err)
			}
		}
	}
	return accountName, subject, nil
}

// Parse an import stream or service.
// e.g.
//   {stream: "public.>"} # No accounts means public.
//   {stream: "synadia.private.>", accounts: [cncf, natsio]}
//   {service: "pub.request"} # No accounts means public.
//   {service: "pub.special.request", accounts: [nats.io]}
func parseExportStreamOrService(v interface{}, errors, warnings *[]error) (*export, *export, error) {
	var (
		curStream  *export
		curService *export
		accounts   []string
	)
	tk, v := unwrapValue(v)
	vv, ok := v.(map[string]interface{})
	if !ok {
		return nil, nil, &configErr{tk, fmt.Sprintf("Export Items should be a map with type entry, got %T", v)}
	}
	for mk, mv := range vv {
		tk, mv := unwrapValue(mv)
		switch strings.ToLower(mk) {
		case "stream":
			if curService != nil {
				err := &configErr{tk, fmt.Sprintf("Detected stream %q but already saw a service", mv)}
				*errors = append(*errors, err)
				continue
			}

			mvs, ok := mv.(string)
			if !ok {
				err := &configErr{tk, fmt.Sprintf("Expected stream name to be string, got %T", mv)}
				*errors = append(*errors, err)
				continue
			}
			curStream = &export{sub: mvs}
			if accounts != nil {
				curStream.accs = accounts
			}
		case "service":
			if curStream != nil {
				err := &configErr{tk, fmt.Sprintf("Detected service %q but already saw a stream", mv)}
				*errors = append(*errors, err)
				continue
			}
			mvs, ok := mv.(string)
			if !ok {
				err := &configErr{tk, fmt.Sprintf("Expected service name to be string, got %T", mv)}
				*errors = append(*errors, err)
				continue
			}
			curService = &export{sub: mvs}
			if accounts != nil {
				curService.accs = accounts
			}
		case "accounts":
			for _, iv := range mv.([]interface{}) {
				_, mv := unwrapValue(iv)
				accounts = append(accounts, mv.(string))
			}
			if curStream != nil {
				curStream.accs = accounts
			} else if curService != nil {
				curService.accs = accounts
			}
		default:
			if !tk.IsUsedVariable() {
				err := &unknownConfigFieldErr{
					field: mk,
					configErr: configErr{
						token: tk,
					},
				}
				*errors = append(*errors, err)
			}
		}

	}
	return curStream, curService, nil
}

// Parse an import stream or service.
// e.g.
//   {stream: {account: "synadia", subject:"public.synadia"}, prefix: "imports.synadia"}
//   {stream: {account: "synadia", subject:"synadia.private.*"}}
//   {service: {account: "synadia", subject: "pub.special.request"}, subject: "synadia.request"}
func parseImportStreamOrService(v interface{}, errors, warnings *[]error) (*importStream, *importService, error) {
	var (
		curStream  *importStream
		curService *importService
		pre, to    string
	)
	tk, mv := unwrapValue(v)
	vv, ok := mv.(map[string]interface{})
	if !ok {
		return nil, nil, &configErr{tk, fmt.Sprintf("Import Items should be a map with type entry, got %T", mv)}
	}
	for mk, mv := range vv {
		tk, mv := unwrapValue(mv)
		switch strings.ToLower(mk) {
		case "stream":
			if curService != nil {
				err := &configErr{tk, fmt.Sprintf("Detected stream but already saw a service")}
				*errors = append(*errors, err)
				continue
			}
			ac, ok := mv.(map[string]interface{})
			if !ok {
				err := &configErr{tk, fmt.Sprintf("Stream entry should be an account map, got %T", mv)}
				*errors = append(*errors, err)
				continue
			}
			// Make sure this is a map with account and subject
			accountName, subject, err := parseAccount(ac, errors, warnings)
			if err != nil {
				*errors = append(*errors, err)
				continue
			}
			if accountName == "" || subject == "" {
				err := &configErr{tk, fmt.Sprintf("Expect an account name and a subject")}
				*errors = append(*errors, err)
				continue
			}
			curStream = &importStream{an: accountName, sub: subject}
			if pre != "" {
				curStream.pre = pre
			}
		case "service":
			if curStream != nil {
				err := &configErr{tk, fmt.Sprintf("Detected service but already saw a stream")}
				*errors = append(*errors, err)
				continue
			}
			ac, ok := mv.(map[string]interface{})
			if !ok {
				err := &configErr{tk, fmt.Sprintf("Service entry should be an account map, got %T", mv)}
				*errors = append(*errors, err)
				continue
			}
			// Make sure this is a map with account and subject
			accountName, subject, err := parseAccount(ac, errors, warnings)
			if err != nil {
				*errors = append(*errors, err)
				continue
			}
			if accountName == "" || subject == "" {
				err := &configErr{tk, fmt.Sprintf("Expect an account name and a subject")}
				*errors = append(*errors, err)
				continue
			}
			curService = &importService{an: accountName, sub: subject}
			if to != "" {
				curService.to = to
			}
		case "prefix":
			pre = mv.(string)
			if curStream != nil {
				curStream.pre = pre
			}
		case "to":
			to = mv.(string)
			if curService != nil {
				curService.to = to
			}
		default:
			if !tk.IsUsedVariable() {
				err := &unknownConfigFieldErr{
					field: mk,
					configErr: configErr{
						token: tk,
					},
				}
				*errors = append(*errors, err)
			}
		}

	}
	return curStream, curService, nil
}

// Helper function to parse Authorization configs.
func parseAuthorization(v interface{}, opts *Options, errors *[]error, warnings *[]error) (*authorization, error) {
	var (
		am   map[string]interface{}
		tk   token
		auth = &authorization{}
	)

	_, v = unwrapValue(v)
	am = v.(map[string]interface{})
	for mk, mv := range am {
		tk, mv = unwrapValue(mv)
		switch strings.ToLower(mk) {
		case "user", "username":
			auth.user = mv.(string)
		case "pass", "password":
			auth.pass = mv.(string)
		case "token":
			auth.token = mv.(string)
		case "timeout":
			at := float64(1)
			switch mv.(type) {
			case int64:
				at = float64(mv.(int64))
			case float64:
				at = mv.(float64)
			}
			auth.timeout = at
		case "users":
			nkeys, users, err := parseUsers(tk, opts, errors, warnings)
			if err != nil {
				*errors = append(*errors, err)
				continue
			}
			auth.users = users
			auth.nkeys = nkeys
		case "default_permission", "default_permissions", "permissions":
			permissions, err := parseUserPermissions(tk, opts, errors, warnings)
			if err != nil {
				*errors = append(*errors, err)
				continue
			}
			auth.defaultPermissions = permissions
		default:
			if !tk.IsUsedVariable() {
				err := &unknownConfigFieldErr{
					field: mk,
					configErr: configErr{
						token: tk,
					},
				}
				*errors = append(*errors, err)
			}
			continue
		}

		// Now check for permission defaults with multiple users, etc.
		if auth.users != nil && auth.defaultPermissions != nil {
			for _, user := range auth.users {
				if user.Permissions == nil {
					user.Permissions = auth.defaultPermissions
				}
			}
		}

	}
	return auth, nil
}

// Helper function to parse multiple users array with optional permissions.
func parseUsers(mv interface{}, opts *Options, errors *[]error, warnings *[]error) ([]*NkeyUser, []*User, error) {
	var (
		tk    token
		keys  []*NkeyUser
		users = []*User{}
	)
	tk, mv = unwrapValue(mv)

	// Make sure we have an array
	uv, ok := mv.([]interface{})
	if !ok {
		return nil, nil, &configErr{tk, fmt.Sprintf("Expected users field to be an array, got %v", mv)}
	}
	for _, u := range uv {
		tk, u = unwrapValue(u)

		// Check its a map/struct
		um, ok := u.(map[string]interface{})
		if !ok {
			err := &configErr{tk, fmt.Sprintf("Expected user entry to be a map/struct, got %v", u)}
			*errors = append(*errors, err)
			continue
		}

		var (
			user  = &User{}
			nkey  = &NkeyUser{}
			perms *Permissions
			err   error
		)
		for k, v := range um {
			// Also needs to unwrap first
			tk, v = unwrapValue(v)

			switch strings.ToLower(k) {
			case "nkey":
				nkey.Nkey = v.(string)
			case "user", "username":
				user.Username = v.(string)
			case "pass", "password":
				user.Password = v.(string)
			case "permission", "permissions", "authorization":
				perms, err = parseUserPermissions(tk, opts, errors, warnings)
				if err != nil {
					*errors = append(*errors, err)
					continue
				}
			default:
				if !tk.IsUsedVariable() {
					err := &unknownConfigFieldErr{
						field: k,
						configErr: configErr{
							token: tk,
						},
					}
					*errors = append(*errors, err)
					continue
				}
			}
		}
		// Place perms if we have them.
		if perms != nil {
			// nkey takes precedent.
			if nkey.Nkey != "" {
				nkey.Permissions = perms
			} else {
				user.Permissions = perms
			}
		}

		// Check to make sure we have at least username and password if defined.
		if nkey.Nkey == "" && (user.Username == "" || user.Password == "") {
			return nil, nil, &configErr{tk, fmt.Sprintf("User entry requires a user and a password")}
		} else if nkey.Nkey != "" {
			// Make sure the nkey a proper public nkey for a user..
			if !nkeys.IsValidPublicUserKey(nkey.Nkey) {
				return nil, nil, &configErr{tk, fmt.Sprintf("Not a valid public nkey for a user")}
			}
			// If we have user or password defined here that is an error.
			if user.Username != "" || user.Password != "" {
				return nil, nil, &configErr{tk, fmt.Sprintf("Nkey users do not take usernames or passwords")}
			}
			keys = append(keys, nkey)
		} else {
			users = append(users, user)
		}
	}
	return keys, users, nil
}

// Helper function to parse user/account permissions
func parseUserPermissions(mv interface{}, opts *Options, errors, warnings *[]error) (*Permissions, error) {
	var (
		tk token
		p  = &Permissions{}
	)
	tk, mv = unwrapValue(mv)
	pm, ok := mv.(map[string]interface{})
	if !ok {
		return nil, &configErr{tk, fmt.Sprintf("Expected permissions to be a map/struct, got %+v", mv)}
	}
	for k, v := range pm {
		tk, v = unwrapValue(v)

		switch strings.ToLower(k) {
		// For routes:
		// Import is Publish
		// Export is Subscribe
		case "pub", "publish", "import":
			perms, err := parseVariablePermissions(v, opts, errors, warnings)
			if err != nil {
				*errors = append(*errors, err)
				continue
			}
			p.Publish = perms
		case "sub", "subscribe", "export":
			perms, err := parseVariablePermissions(v, opts, errors, warnings)
			if err != nil {
				*errors = append(*errors, err)
				continue
			}
			p.Subscribe = perms
		default:
			if !tk.IsUsedVariable() {
				err := &configErr{tk, fmt.Sprintf("Unknown field %q parsing permissions", k)}
				*errors = append(*errors, err)
			}
		}
	}
	return p, nil
}

// Top level parser for authorization configurations.
func parseVariablePermissions(v interface{}, opts *Options, errors, warnings *[]error) (*SubjectPermission, error) {
	switch vv := v.(type) {
	case map[string]interface{}:
		// New style with allow and/or deny properties.
		return parseSubjectPermission(vv, opts, errors, warnings)
	default:
		// Old style
		return parseOldPermissionStyle(v, errors, warnings)
	}
}

// Helper function to parse subject singeltons and/or arrays
func parseSubjects(v interface{}, errors, warnings *[]error) ([]string, error) {
	tk, v := unwrapValue(v)

	var subjects []string
	switch vv := v.(type) {
	case string:
		subjects = append(subjects, vv)
	case []string:
		subjects = vv
	case []interface{}:
		for _, i := range vv {
			tk, i := unwrapValue(i)

			subject, ok := i.(string)
			if !ok {
				return nil, &configErr{tk, fmt.Sprintf("Subject in permissions array cannot be cast to string")}
			}
			subjects = append(subjects, subject)
		}
	default:
		return nil, &configErr{tk, fmt.Sprintf("Expected subject permissions to be a subject, or array of subjects, got %T", v)}
	}
	if err := checkSubjectArray(subjects); err != nil {
		return nil, &configErr{tk, err.Error()}
	}
	return subjects, nil
}

// Helper function to parse old style authorization configs.
func parseOldPermissionStyle(v interface{}, errors, warnings *[]error) (*SubjectPermission, error) {
	subjects, err := parseSubjects(v, errors, warnings)
	if err != nil {
		return nil, err
	}
	return &SubjectPermission{Allow: subjects}, nil
}

// Helper function to parse new style authorization into a SubjectPermission with Allow and Deny.
func parseSubjectPermission(v interface{}, opts *Options, errors, warnings *[]error) (*SubjectPermission, error) {
	m := v.(map[string]interface{})
	if len(m) == 0 {
		return nil, nil
	}
	p := &SubjectPermission{}
	for k, v := range m {
		tk, _ := unwrapValue(v)
		switch strings.ToLower(k) {
		case "allow":
			subjects, err := parseSubjects(tk, errors, warnings)
			if err != nil {
				*errors = append(*errors, err)
				continue
			}
			p.Allow = subjects
		case "deny":
			subjects, err := parseSubjects(tk, errors, warnings)
			if err != nil {
				*errors = append(*errors, err)
				continue
			}
			p.Deny = subjects
		default:
			if !tk.IsUsedVariable() {
				err := &configErr{tk, fmt.Sprintf("Unknown field name %q parsing subject permissions, only 'allow' or 'deny' are permitted", k)}
				*errors = append(*errors, err)
			}
		}
	}
	return p, nil
}

// Helper function to validate subjects, etc for account permissioning.
func checkSubjectArray(sa []string) error {
	for _, s := range sa {
		if !IsValidSubject(s) {
			return fmt.Errorf("Subject %q is not a valid subject", s)
		}
	}
	return nil
}

// PrintTLSHelpAndDie prints TLS usage and exits.
func PrintTLSHelpAndDie() {
	fmt.Printf("%s", tlsUsage)
	for k := range cipherMap {
		fmt.Printf("    %s\n", k)
	}
	fmt.Printf("\nAvailable curve preferences include:\n")
	for k := range curvePreferenceMap {
		fmt.Printf("    %s\n", k)
	}
	os.Exit(0)
}

func parseCipher(cipherName string) (uint16, error) {
	cipher, exists := cipherMap[cipherName]
	if !exists {
		return 0, fmt.Errorf("Unrecognized cipher %s", cipherName)
	}

	return cipher, nil
}

func parseCurvePreferences(curveName string) (tls.CurveID, error) {
	curve, exists := curvePreferenceMap[curveName]
	if !exists {
		return 0, fmt.Errorf("Unrecognized curve preference %s", curveName)
	}
	return curve, nil
}

// Helper function to parse TLS configs.
func parseTLS(v interface{}, opts *Options) (*TLSConfigOpts, error) {
	var (
		tlsm map[string]interface{}
		tc   = TLSConfigOpts{}
	)
	_, v = unwrapValue(v)
	tlsm = v.(map[string]interface{})
	for mk, mv := range tlsm {
		tk, mv := unwrapValue(mv)
		switch strings.ToLower(mk) {
		case "cert_file":
			certFile, ok := mv.(string)
			if !ok {
				return nil, &configErr{tk, fmt.Sprintf("error parsing tls config, expected 'cert_file' to be filename")}
			}
			tc.CertFile = certFile
		case "key_file":
			keyFile, ok := mv.(string)
			if !ok {
				return nil, &configErr{tk, fmt.Sprintf("error parsing tls config, expected 'key_file' to be filename")}
			}
			tc.KeyFile = keyFile
		case "ca_file":
			caFile, ok := mv.(string)
			if !ok {
				return nil, &configErr{tk, fmt.Sprintf("error parsing tls config, expected 'ca_file' to be filename")}
			}
			tc.CaFile = caFile
		case "verify":
			verify, ok := mv.(bool)
			if !ok {
				return nil, &configErr{tk, fmt.Sprintf("error parsing tls config, expected 'verify' to be a boolean")}
			}
			tc.Verify = verify
		case "cipher_suites":
			ra := mv.([]interface{})
			if len(ra) == 0 {
				return nil, &configErr{tk, fmt.Sprintf("error parsing tls config, 'cipher_suites' cannot be empty")}
			}
			tc.Ciphers = make([]uint16, 0, len(ra))
			for _, r := range ra {
				tk, r := unwrapValue(r)
				cipher, err := parseCipher(r.(string))
				if err != nil {
					return nil, &configErr{tk, err.Error()}
				}
				tc.Ciphers = append(tc.Ciphers, cipher)
			}
		case "curve_preferences":
			ra := mv.([]interface{})
			if len(ra) == 0 {
				return nil, &configErr{tk, fmt.Sprintf("error parsing tls config, 'curve_preferences' cannot be empty")}
			}
			tc.CurvePreferences = make([]tls.CurveID, 0, len(ra))
			for _, r := range ra {
				tk, r := unwrapValue(r)
				cps, err := parseCurvePreferences(r.(string))
				if err != nil {
					return nil, &configErr{tk, err.Error()}
				}
				tc.CurvePreferences = append(tc.CurvePreferences, cps)
			}
		case "timeout":
			at := float64(0)
			switch mv.(type) {
			case int64:
				at = float64(mv.(int64))
			case float64:
				at = mv.(float64)
			}
			tc.Timeout = at
		default:
			return nil, &configErr{tk, fmt.Sprintf("error parsing tls config, unknown field [%q]", mk)}
		}
	}

	// If cipher suites were not specified then use the defaults
	if tc.Ciphers == nil {
		tc.Ciphers = defaultCipherSuites()
	}

	// If curve preferences were not specified, then use the defaults
	if tc.CurvePreferences == nil {
		tc.CurvePreferences = defaultCurvePreferences()
	}

	return &tc, nil
}

// GenTLSConfig loads TLS related configuration parameters.
func GenTLSConfig(tc *TLSConfigOpts) (*tls.Config, error) {

	// Now load in cert and private key
	cert, err := tls.LoadX509KeyPair(tc.CertFile, tc.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("error parsing X509 certificate/key pair: %v", err)
	}
	cert.Leaf, err = x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("error parsing certificate: %v", err)
	}

	// Create the tls.Config from our options.
	// We will determine the cipher suites that we prefer.
	// FIXME(dlc) change if ARM based.
	config := tls.Config{
		MinVersion:               tls.VersionTLS12,
		CipherSuites:             tc.Ciphers,
		PreferServerCipherSuites: true,
		CurvePreferences:         tc.CurvePreferences,
		Certificates:             []tls.Certificate{cert},
	}

	// Require client certificates as needed
	if tc.Verify {
		config.ClientAuth = tls.RequireAndVerifyClientCert
	}
	// Add in CAs if applicable.
	if tc.CaFile != "" {
		rootPEM, err := ioutil.ReadFile(tc.CaFile)
		if err != nil || rootPEM == nil {
			return nil, err
		}
		pool := x509.NewCertPool()
		ok := pool.AppendCertsFromPEM(rootPEM)
		if !ok {
			return nil, fmt.Errorf("failed to parse root ca certificate")
		}
		config.ClientCAs = pool
	}

	return &config, nil
}

// MergeOptions will merge two options giving preference to the flagOpts
// if the item is present.
func MergeOptions(fileOpts, flagOpts *Options) *Options {
	if fileOpts == nil {
		return flagOpts
	}
	if flagOpts == nil {
		return fileOpts
	}
	// Merge the two, flagOpts override
	opts := *fileOpts

	if flagOpts.Port != 0 {
		opts.Port = flagOpts.Port
	}
	if flagOpts.Host != "" {
		opts.Host = flagOpts.Host
	}
	if flagOpts.ClientAdvertise != "" {
		opts.ClientAdvertise = flagOpts.ClientAdvertise
	}
	if flagOpts.Username != "" {
		opts.Username = flagOpts.Username
	}
	if flagOpts.Password != "" {
		opts.Password = flagOpts.Password
	}
	if flagOpts.Authorization != "" {
		opts.Authorization = flagOpts.Authorization
	}
	if flagOpts.HTTPPort != 0 {
		opts.HTTPPort = flagOpts.HTTPPort
	}
	if flagOpts.Debug {
		opts.Debug = true
	}
	if flagOpts.Trace {
		opts.Trace = true
	}
	if flagOpts.Logtime {
		opts.Logtime = true
	}
	if flagOpts.LogFile != "" {
		opts.LogFile = flagOpts.LogFile
	}
	if flagOpts.PidFile != "" {
		opts.PidFile = flagOpts.PidFile
	}
	if flagOpts.PortsFileDir != "" {
		opts.PortsFileDir = flagOpts.PortsFileDir
	}
	if flagOpts.ProfPort != 0 {
		opts.ProfPort = flagOpts.ProfPort
	}
	if flagOpts.Cluster.ListenStr != "" {
		opts.Cluster.ListenStr = flagOpts.Cluster.ListenStr
	}
	if flagOpts.Cluster.NoAdvertise {
		opts.Cluster.NoAdvertise = true
	}
	if flagOpts.Cluster.ConnectRetries != 0 {
		opts.Cluster.ConnectRetries = flagOpts.Cluster.ConnectRetries
	}
	if flagOpts.Cluster.Advertise != "" {
		opts.Cluster.Advertise = flagOpts.Cluster.Advertise
	}
	if flagOpts.RoutesStr != "" {
		mergeRoutes(&opts, flagOpts)
	}
	return &opts
}

// RoutesFromStr parses route URLs from a string
func RoutesFromStr(routesStr string) []*url.URL {
	routes := strings.Split(routesStr, ",")
	if len(routes) == 0 {
		return nil
	}
	routeUrls := []*url.URL{}
	for _, r := range routes {
		r = strings.TrimSpace(r)
		u, _ := url.Parse(r)
		routeUrls = append(routeUrls, u)
	}
	return routeUrls
}

// This will merge the flag routes and override anything that was present.
func mergeRoutes(opts, flagOpts *Options) {
	routeUrls := RoutesFromStr(flagOpts.RoutesStr)
	if routeUrls == nil {
		return
	}
	opts.Routes = routeUrls
	opts.RoutesStr = flagOpts.RoutesStr
}

// RemoveSelfReference removes this server from an array of routes
func RemoveSelfReference(clusterPort int, routes []*url.URL) ([]*url.URL, error) {
	var cleanRoutes []*url.URL
	cport := strconv.Itoa(clusterPort)

	selfIPs, err := getInterfaceIPs()
	if err != nil {
		return nil, err
	}
	for _, r := range routes {
		host, port, err := net.SplitHostPort(r.Host)
		if err != nil {
			return nil, err
		}

		ipList, err := getURLIP(host)
		if err != nil {
			return nil, err
		}
		if cport == port && isIPInList(selfIPs, ipList) {
			continue
		}
		cleanRoutes = append(cleanRoutes, r)
	}

	return cleanRoutes, nil
}

func isIPInList(list1 []net.IP, list2 []net.IP) bool {
	for _, ip1 := range list1 {
		for _, ip2 := range list2 {
			if ip1.Equal(ip2) {
				return true
			}
		}
	}
	return false
}

func getURLIP(ipStr string) ([]net.IP, error) {
	ipList := []net.IP{}

	ip := net.ParseIP(ipStr)
	if ip != nil {
		ipList = append(ipList, ip)
		return ipList, nil
	}

	hostAddr, err := net.LookupHost(ipStr)
	if err != nil {
		return nil, fmt.Errorf("Error looking up host with route hostname: %v", err)
	}
	for _, addr := range hostAddr {
		ip = net.ParseIP(addr)
		if ip != nil {
			ipList = append(ipList, ip)
		}
	}
	return ipList, nil
}

func getInterfaceIPs() ([]net.IP, error) {
	var localIPs []net.IP

	interfaceAddr, err := net.InterfaceAddrs()
	if err != nil {
		return nil, fmt.Errorf("Error getting self referencing address: %v", err)
	}

	for i := 0; i < len(interfaceAddr); i++ {
		interfaceIP, _, _ := net.ParseCIDR(interfaceAddr[i].String())
		if net.ParseIP(interfaceIP.String()) != nil {
			localIPs = append(localIPs, interfaceIP)
		} else {
			return nil, fmt.Errorf("Error parsing self referencing address: %v", err)
		}
	}
	return localIPs, nil
}

func processOptions(opts *Options) {
	// Setup non-standard Go defaults
	if opts.Host == "" {
		opts.Host = DEFAULT_HOST
	}
	if opts.HTTPHost == "" {
		// Default to same bind from server if left undefined
		opts.HTTPHost = opts.Host
	}
	if opts.Port == 0 {
		opts.Port = DEFAULT_PORT
	} else if opts.Port == RANDOM_PORT {
		// Choose randomly inside of net.Listen
		opts.Port = 0
	}
	if opts.MaxConn == 0 {
		opts.MaxConn = DEFAULT_MAX_CONNECTIONS
	}
	if opts.PingInterval == 0 {
		opts.PingInterval = DEFAULT_PING_INTERVAL
	}
	if opts.MaxPingsOut == 0 {
		opts.MaxPingsOut = DEFAULT_PING_MAX_OUT
	}
	if opts.TLSTimeout == 0 {
		opts.TLSTimeout = float64(TLS_TIMEOUT) / float64(time.Second)
	}
	if opts.AuthTimeout == 0 {
		opts.AuthTimeout = float64(AUTH_TIMEOUT) / float64(time.Second)
	}
	if opts.Cluster.Port != 0 {
		if opts.Cluster.Host == "" {
			opts.Cluster.Host = DEFAULT_HOST
		}
		if opts.Cluster.TLSTimeout == 0 {
			opts.Cluster.TLSTimeout = float64(TLS_TIMEOUT) / float64(time.Second)
		}
		if opts.Cluster.AuthTimeout == 0 {
			opts.Cluster.AuthTimeout = float64(AUTH_TIMEOUT) / float64(time.Second)
		}
	}
	if opts.MaxControlLine == 0 {
		opts.MaxControlLine = MAX_CONTROL_LINE_SIZE
	}
	if opts.MaxPayload == 0 {
		opts.MaxPayload = MAX_PAYLOAD_SIZE
	}
	if opts.MaxPending == 0 {
		opts.MaxPending = MAX_PENDING_SIZE
	}
	if opts.WriteDeadline == time.Duration(0) {
		opts.WriteDeadline = DEFAULT_FLUSH_DEADLINE
	}
	if opts.RQSubsSweep == time.Duration(0) {
		opts.RQSubsSweep = DEFAULT_REMOTE_QSUBS_SWEEPER
	}
	if opts.MaxClosedClients == 0 {
		opts.MaxClosedClients = DEFAULT_MAX_CLOSED_CLIENTS
	}
	if opts.LameDuckDuration == 0 {
		opts.LameDuckDuration = DEFAULT_LAME_DUCK_DURATION
	}
}

// ConfigureOptions accepts a flag set and augment it with NATS Server
// specific flags. On success, an options structure is returned configured
// based on the selected flags and/or configuration file.
// The command line options take precedence to the ones in the configuration file.
func ConfigureOptions(fs *flag.FlagSet, args []string, printVersion, printHelp, printTLSHelp func()) (*Options, error) {
	opts := &Options{}
	var (
		showVersion bool
		showHelp    bool
		showTLSHelp bool
		signal      string
		configFile  string
		err         error
	)

	fs.BoolVar(&showHelp, "h", false, "Show this message.")
	fs.BoolVar(&showHelp, "help", false, "Show this message.")
	fs.IntVar(&opts.Port, "port", 0, "Port to listen on.")
	fs.IntVar(&opts.Port, "p", 0, "Port to listen on.")
	fs.StringVar(&opts.Host, "addr", "", "Network host to listen on.")
	fs.StringVar(&opts.Host, "a", "", "Network host to listen on.")
	fs.StringVar(&opts.Host, "net", "", "Network host to listen on.")
	fs.StringVar(&opts.ClientAdvertise, "client_advertise", "", "Client URL to advertise to other servers.")
	fs.BoolVar(&opts.Debug, "D", false, "Enable Debug logging.")
	fs.BoolVar(&opts.Debug, "debug", false, "Enable Debug logging.")
	fs.BoolVar(&opts.Trace, "V", false, "Enable Trace logging.")
	fs.BoolVar(&opts.Trace, "trace", false, "Enable Trace logging.")
	fs.Bool("DV", false, "Enable Debug and Trace logging.")
	fs.BoolVar(&opts.Logtime, "T", true, "Timestamp log entries.")
	fs.BoolVar(&opts.Logtime, "logtime", true, "Timestamp log entries.")
	fs.StringVar(&opts.Username, "user", "", "Username required for connection.")
	fs.StringVar(&opts.Password, "pass", "", "Password required for connection.")
	fs.StringVar(&opts.Authorization, "auth", "", "Authorization token required for connection.")
	fs.IntVar(&opts.HTTPPort, "m", 0, "HTTP Port for /varz, /connz endpoints.")
	fs.IntVar(&opts.HTTPPort, "http_port", 0, "HTTP Port for /varz, /connz endpoints.")
	fs.IntVar(&opts.HTTPSPort, "ms", 0, "HTTPS Port for /varz, /connz endpoints.")
	fs.IntVar(&opts.HTTPSPort, "https_port", 0, "HTTPS Port for /varz, /connz endpoints.")
	fs.StringVar(&configFile, "c", "", "Configuration file.")
	fs.StringVar(&configFile, "config", "", "Configuration file.")
	fs.BoolVar(&opts.CheckConfig, "t", false, "Check configuration and exit.")
	fs.StringVar(&signal, "sl", "", "Send signal to gnatsd process (stop, quit, reopen, reload)")
	fs.StringVar(&signal, "signal", "", "Send signal to gnatsd process (stop, quit, reopen, reload)")
	fs.StringVar(&opts.PidFile, "P", "", "File to store process pid.")
	fs.StringVar(&opts.PidFile, "pid", "", "File to store process pid.")
	fs.StringVar(&opts.PortsFileDir, "ports_file_dir", "", "Creates a ports file in the specified directory (<executable_name>_<pid>.ports)")
	fs.StringVar(&opts.LogFile, "l", "", "File to store logging output.")
	fs.StringVar(&opts.LogFile, "log", "", "File to store logging output.")
	fs.BoolVar(&opts.Syslog, "s", false, "Enable syslog as log method.")
	fs.BoolVar(&opts.Syslog, "syslog", false, "Enable syslog as log method..")
	fs.StringVar(&opts.RemoteSyslog, "r", "", "Syslog server addr (udp://127.0.0.1:514).")
	fs.StringVar(&opts.RemoteSyslog, "remote_syslog", "", "Syslog server addr (udp://127.0.0.1:514).")
	fs.BoolVar(&showVersion, "version", false, "Print version information.")
	fs.BoolVar(&showVersion, "v", false, "Print version information.")
	fs.IntVar(&opts.ProfPort, "profile", 0, "Profiling HTTP port")
	fs.StringVar(&opts.RoutesStr, "routes", "", "Routes to actively solicit a connection.")
	fs.StringVar(&opts.Cluster.ListenStr, "cluster", "", "Cluster url from which members can solicit routes.")
	fs.StringVar(&opts.Cluster.ListenStr, "cluster_listen", "", "Cluster url from which members can solicit routes.")
	fs.StringVar(&opts.Cluster.Advertise, "cluster_advertise", "", "Cluster URL to advertise to other servers.")
	fs.BoolVar(&opts.Cluster.NoAdvertise, "no_advertise", false, "Advertise known cluster IPs to clients.")
	fs.IntVar(&opts.Cluster.ConnectRetries, "connect_retries", 0, "For implicit routes, number of connect retries")
	fs.BoolVar(&showTLSHelp, "help_tls", false, "TLS help.")
	fs.BoolVar(&opts.TLS, "tls", false, "Enable TLS.")
	fs.BoolVar(&opts.TLSVerify, "tlsverify", false, "Enable TLS with client verification.")
	fs.StringVar(&opts.TLSCert, "tlscert", "", "Server certificate file.")
	fs.StringVar(&opts.TLSKey, "tlskey", "", "Private key for server certificate.")
	fs.StringVar(&opts.TLSCaCert, "tlscacert", "", "Client certificate CA for verification.")

	// The flags definition above set "default" values to some of the options.
	// Calling Parse() here will override the default options with any value
	// specified from the command line. This is ok. We will then update the
	// options with the content of the configuration file (if present), and then,
	// call Parse() again to override the default+config with command line values.
	// Calling Parse() before processing config file is necessary since configFile
	// itself is a command line argument, and also Parse() is required in order
	// to know if user wants simply to show "help" or "version", etc...
	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	if showVersion {
		printVersion()
		return nil, nil
	}

	if showHelp {
		printHelp()
		return nil, nil
	}

	if showTLSHelp {
		printTLSHelp()
		return nil, nil
	}

	// Process args looking for non-flag options,
	// 'version' and 'help' only for now
	showVersion, showHelp, err = ProcessCommandLineArgs(fs)
	if err != nil {
		return nil, err
	} else if showVersion {
		printVersion()
		return nil, nil
	} else if showHelp {
		printHelp()
		return nil, nil
	}

	// Snapshot flag options.
	FlagSnapshot = opts.Clone()

	// Process signal control.
	if signal != "" {
		if err := processSignal(signal); err != nil {
			return nil, err
		}
	}

	// Parse config if given
	if configFile != "" {
		// This will update the options with values from the config file.
		err := opts.ProcessConfigFile(configFile)
		if err != nil {
			if opts.CheckConfig {
				return nil, err
			}

			// If only warnings then can still continue.
			if cerr, ok := err.(*processConfigErr); ok && len(cerr.Errors()) == 0 {
				fmt.Fprint(os.Stderr, err)
				return opts, nil
			}

			return nil, err
		} else if opts.CheckConfig {
			// Report configuration file syntax test was successful and exit.
			return opts, nil
		}

		// Call this again to override config file options with options from command line.
		// Note: We don't need to check error here since if there was an error, it would
		// have been caught the first time this function was called (after setting up the
		// flags).
		fs.Parse(args)
	} else if opts.CheckConfig {
		return nil, fmt.Errorf("must specify [-c, --config] option to check configuration file syntax")
	}

	// Special handling of some flags
	var (
		flagErr     error
		tlsDisabled bool
		tlsOverride bool
	)
	fs.Visit(func(f *flag.Flag) {
		// short-circuit if an error was encountered
		if flagErr != nil {
			return
		}
		if strings.HasPrefix(f.Name, "tls") {
			if f.Name == "tls" {
				if !opts.TLS {
					// User has specified "-tls=false", we need to disable TLS
					opts.TLSConfig = nil
					tlsDisabled = true
					tlsOverride = false
					return
				}
				tlsOverride = true
			} else if !tlsDisabled {
				tlsOverride = true
			}
		} else {
			switch f.Name {
			case "DV":
				// Check value to support -DV=false
				boolValue, _ := strconv.ParseBool(f.Value.String())
				opts.Trace, opts.Debug = boolValue, boolValue
			case "cluster", "cluster_listen":
				// Override cluster config if explicitly set via flags.
				flagErr = overrideCluster(opts)
			case "routes":
				// Keep in mind that the flag has updated opts.RoutesStr at this point.
				if opts.RoutesStr == "" {
					// Set routes array to nil since routes string is empty
					opts.Routes = nil
					return
				}
				routeUrls := RoutesFromStr(opts.RoutesStr)
				opts.Routes = routeUrls
			}
		}
	})
	if flagErr != nil {
		return nil, flagErr
	}

	// This will be true if some of the `-tls` params have been set and
	// `-tls=false` has not been set.
	if tlsOverride {
		if err := overrideTLS(opts); err != nil {
			return nil, err
		}
	}

	// If we don't have cluster defined in the configuration
	// file and no cluster listen string override, but we do
	// have a routes override, we need to report misconfiguration.
	if opts.RoutesStr != "" && opts.Cluster.ListenStr == "" && opts.Cluster.Host == "" && opts.Cluster.Port == 0 {
		return nil, errors.New("solicited routes require cluster capabilities, e.g. --cluster")
	}

	return opts, nil
}

// overrideTLS is called when at least "-tls=true" has been set.
func overrideTLS(opts *Options) error {
	if opts.TLSCert == "" {
		return errors.New("TLS Server certificate must be present and valid")
	}
	if opts.TLSKey == "" {
		return errors.New("TLS Server private key must be present and valid")
	}

	tc := TLSConfigOpts{}
	tc.CertFile = opts.TLSCert
	tc.KeyFile = opts.TLSKey
	tc.CaFile = opts.TLSCaCert
	tc.Verify = opts.TLSVerify

	var err error
	opts.TLSConfig, err = GenTLSConfig(&tc)
	return err
}

// overrideCluster updates Options.Cluster if that flag "cluster" (or "cluster_listen")
// has explicitly be set in the command line. If it is set to empty string, it will
// clear the Cluster options.
func overrideCluster(opts *Options) error {
	if opts.Cluster.ListenStr == "" {
		// This one is enough to disable clustering.
		opts.Cluster.Port = 0
		return nil
	}
	clusterURL, err := url.Parse(opts.Cluster.ListenStr)
	if err != nil {
		return err
	}
	h, p, err := net.SplitHostPort(clusterURL.Host)
	if err != nil {
		return err
	}
	opts.Cluster.Host = h
	_, err = fmt.Sscan(p, &opts.Cluster.Port)
	if err != nil {
		return err
	}

	if clusterURL.User != nil {
		pass, hasPassword := clusterURL.User.Password()
		if !hasPassword {
			return errors.New("expected cluster password to be set")
		}
		opts.Cluster.Password = pass

		user := clusterURL.User.Username()
		opts.Cluster.Username = user
	} else {
		// Since we override from flag and there is no user/pwd, make
		// sure we clear what we may have gotten from config file.
		opts.Cluster.Username = ""
		opts.Cluster.Password = ""
	}

	return nil
}

func processSignal(signal string) error {
	var (
		pid           string
		commandAndPid = strings.Split(signal, "=")
	)
	if l := len(commandAndPid); l == 2 {
		pid = commandAndPid[1]
	} else if l > 2 {
		return fmt.Errorf("invalid signal parameters: %v", commandAndPid[2:])
	}
	if err := ProcessSignal(Command(commandAndPid[0]), pid); err != nil {
		return err
	}
	os.Exit(0)
	return nil
}
