// Package sshtun provides a SSH tunnel with port forwarding.
package sshtun

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/sync/errgroup"
)

// SSHTun represents a SSH tunnel
type SSHTun struct {
	mutex                 *sync.Mutex
	ctx                   context.Context
	cancel                context.CancelFunc
	started               bool
	user                  string
	authType              AuthType
	authKeyFile           string
	authKeyReader         io.Reader
	authPassword          string
	server                *Endpoint
	local                 *Endpoint
	remote                *Endpoint
	forwardType           ForwardType
	timeout               time.Duration
	connState             func(*SSHTun, ConnState)
	tunneledConnState     func(*SSHTun, *TunneledConnState)
	active                int
	sshClient             *ssh.Client
	sshConfig             *ssh.ClientConfig
	sshConfigKeyExchanges []string
	sshConfigCiphers      []string
	sshConfigMACs         []string

	name string
}

// ForwardType is the type of port forwarding.
// Local: forward from localhost.
// Remote: forward from remote - reverse port forward.
type ForwardType int

const (
	Local ForwardType = iota
	Remote
)

// ConnState represents the state of the SSH tunnel. It's returned to an optional function provided to SetConnState.
type ConnState int

const (
	// StateStopped represents a stopped tunnel. A call to Start will make the state to transition to StateStarting.
	StateStopped ConnState = iota

	// StateStarting represents a tunnel initializing and preparing to listen for connections.
	// A successful initialization will make the state to transition to StateStarted, otherwise it will transition to StateStopped.
	StateStarting

	// StateStarted represents a tunnel ready to accept connections.
	// A call to stop or an error will make the state to transition to StateStopped.
	StateStarted
)

// New creates a new SSH tunnel to the specified server redirecting a port on local localhost to a port on remote localhost.
// By default the SSH connection is made to port 22 as root and using automatic detection of the authentication
// method (see Start for details on this).
// Calling SetPassword will change the authentication to password based.
// Calling SetKeyFile will change the authentication to keyfile based..
// The SSH user and port can be changed with SetUser and SetPort.
// The local and remote hosts can be changed to something different than localhost with SetLocalEndpoint and SetRemoteEndpoint.
// The forward type can be changed with SetForwardType.
// The states of the tunnel can be received throgh a callback function with SetConnState.
// The states of the tunneled connections can be received through a callback function with SetTunneledConnState.
func New(localPort int, server string, remotePort int) *SSHTun {
	sshTun := defaultSSHTun(server)
	sshTun.local = NewTCPEndpoint("localhost", localPort)
	sshTun.remote = NewTCPEndpoint("localhost", remotePort)
	return sshTun
}

// NewRemote does the same as New but for a remote port forward.
func NewRemote(localPort int, server string, remotePort int) *SSHTun {
	sshTun := New(localPort, server, remotePort)
	sshTun.forwardType = Remote
	return sshTun
}

// NewUnix does the same as New but using unix sockets.
func NewUnix(localUnixSocket string, server string, remoteUnixSocket string) *SSHTun {
	sshTun := defaultSSHTun(server)
	sshTun.local = NewUnixEndpoint(localUnixSocket)
	sshTun.remote = NewUnixEndpoint(remoteUnixSocket)
	return sshTun
}

// NewUnixRemote does the same as NewRemote but using unix sockets.
func NewUnixRemote(localUnixSocket string, server string, remoteUnixSocket string) *SSHTun {
	sshTun := NewUnix(localUnixSocket, server, remoteUnixSocket)
	sshTun.forwardType = Remote
	return sshTun
}

func defaultSSHTun(server string) *SSHTun {
	return &SSHTun{
		mutex:       &sync.Mutex{},
		server:      NewTCPEndpoint(server, 22),
		user:        "root",
		authType:    AuthTypeAuto,
		timeout:     time.Second * 15,
		forwardType: Local,
	}
}

// SetPort changes the port where the SSH connection will be made.
func (tun *SSHTun) SetPort(port int) {
	tun.server.port = port
}

// Set KeyExchanges
// supported, forbidden and preferred algos are in https://pkg.go.dev/golang.org/x/crypto/ssh#Config
func (tun *SSHTun) SetKeyExchanges(keyExchanges []string) {
	tun.sshConfigKeyExchanges = keyExchanges
}

// Set ssh Ciphers
// preferred and supported ciphers are in https://pkg.go.dev/golang.org/x/crypto/ssh#Config
func (tun *SSHTun) SetCiphers(ciphers []string) {
	tun.sshConfigCiphers = ciphers
}

// Set MACs
// supported MACs are in https://pkg.go.dev/golang.org/x/crypto/ssh#Config
func (tun *SSHTun) SetMACs(MACs []string) {
	tun.sshConfigMACs = MACs
}

// SetUser changes the user used to make the SSH connection.
func (tun *SSHTun) SetUser(user string) {
	tun.user = user
}

// SetKeyFile changes the authentication to key-based and uses the specified file.
// Leaving the file empty defaults to the default linux private key locations: `~/.ssh/id_rsa`, `~/.ssh/id_dsa`,
// `~/.ssh/id_ecdsa`, `~/.ssh/id_ecdsa_sk`, `~/.ssh/id_ed25519` and `~/.ssh/id_ed25519_sk`.
func (tun *SSHTun) SetKeyFile(file string) {
	tun.authType = AuthTypeKeyFile
	tun.authKeyFile = file
}

// SetEncryptedKeyFile changes the authentication to encrypted key-based and uses the specified file and password.
// Leaving the file empty defaults to the default linux private key locations: `~/.ssh/id_rsa`, `~/.ssh/id_dsa`,
// `~/.ssh/id_ecdsa`, `~/.ssh/id_ecdsa_sk`, `~/.ssh/id_ed25519` and `~/.ssh/id_ed25519_sk`.
func (tun *SSHTun) SetEncryptedKeyFile(file string, password string) {
	tun.authType = AuthTypeEncryptedKeyFile
	tun.authKeyFile = file
	tun.authPassword = password
}

// SetKeyReader changes the authentication to key-based and uses the specified reader.
func (tun *SSHTun) SetKeyReader(reader io.Reader) {
	tun.authType = AuthTypeKeyReader
	tun.authKeyReader = reader
}

// SetEncryptedKeyReader changes the authentication to encrypted key-based and uses the specified reader and password.
func (tun *SSHTun) SetEncryptedKeyReader(reader io.Reader, password string) {
	tun.authType = AuthTypeEncryptedKeyReader
	tun.authKeyReader = reader
	tun.authPassword = password
}

// SetForwardType changes the forward type.
func (tun *SSHTun) SetForwardType(forwardType ForwardType) {
	tun.forwardType = forwardType
}

// SetSSHAgent changes the authentication to ssh-agent.
func (tun *SSHTun) SetSSHAgent() {
	tun.authType = AuthTypeSSHAgent
}

// SetPassword changes the authentication to password-based and uses the specified password.
func (tun *SSHTun) SetPassword(password string) {
	tun.authType = AuthTypePassword
	tun.authPassword = password
}

// SetLocalHost sets the local host to redirect (defaults to localhost).
func (tun *SSHTun) SetLocalHost(host string) {
	tun.local.host = host
}

// SetRemoteHost sets the remote host to redirect (defaults to localhost).
func (tun *SSHTun) SetRemoteHost(host string) {
	tun.remote.host = host
}

// SetLocalEndpoint sets the local endpoint to redirect.
func (tun *SSHTun) SetLocalEndpoint(endpoint *Endpoint) {
	tun.local = endpoint
}

// SetRemoteEndpoint sets the remote endpoint to redirect.
func (tun *SSHTun) SetRemoteEndpoint(endpoint *Endpoint) {
	tun.remote = endpoint
}

// SetTimeout sets the connection timeouts (defaults to 15 seconds).
func (tun *SSHTun) SetTimeout(timeout time.Duration) {
	tun.timeout = timeout
}

// SetConnState specifies an optional callback function that is called when a SSH tunnel changes state.
// See the ConnState type and associated constants for details.
func (tun *SSHTun) SetConnState(connStateFun func(*SSHTun, ConnState)) {
	tun.connState = connStateFun
}

// SetTunneledConnState specifies an optional callback function that is called when the underlying tunneled
// connections change state.
func (tun *SSHTun) SetTunneledConnState(tunneledConnStateFun func(*SSHTun, *TunneledConnState)) {
	tun.tunneledConnState = tunneledConnStateFun
}

// Start starts the SSH tunnel. It can be stopped by calling `Stop` or cancelling its context.
// This call will block until the tunnel is stopped either calling those methods or by an error.
// Note on SSH authentication: in case the tunnel's authType is set to AuthTypeAuto the following will happen:
// The default key files will be used, if that doesn't succeed it will try to use the SSH agent.
// If that fails the whole authentication fails.
// That means if you want to use password or encrypted key file authentication, you have to specify that explicitly.
func (tun *SSHTun) Start(ctx context.Context) error {
	tun.mutex.Lock()
	if tun.started {
		tun.mutex.Unlock()
		return fmt.Errorf("already started")
	}
	tun.started = true
	tun.ctx, tun.cancel = context.WithCancel(ctx)
	tun.mutex.Unlock()

	if tun.connState != nil {
		tun.connState(tun, StateStarting)
	}

	config, err := tun.initSSHConfig()
	if err != nil {
		return tun.stop(fmt.Errorf("ssh config failed: %w", err))
	}
	tun.sshConfig = config

	listenConfig := net.ListenConfig{}
	var listener net.Listener

	if tun.forwardType == Local {
		listener, err = listenConfig.Listen(tun.ctx, tun.local.Type(), tun.local.String())
		if err != nil {
			return tun.stop(fmt.Errorf("local listen %s on %s failed: %w", tun.local.Type(), tun.local.String(), err))
		}
	} else if tun.forwardType == Remote {
		sshClient, err := ssh.Dial(tun.server.Type(), tun.server.String(), tun.sshConfig)
		if err != nil {
			return tun.stop(fmt.Errorf("ssh dial %s to %s failed: %w", tun.server.Type(), tun.server.String(), err))
		}
		listener, err = sshClient.Listen(tun.remote.Type(), tun.remote.String())
		if err != nil {
			return tun.stop(fmt.Errorf("remote listen %s on %s failed: %w", tun.remote.Type(), tun.remote.String(), err))
		}
	}

	errChan := make(chan error)
	go func() {
		errChan <- tun.listen(listener)
	}()

	if tun.connState != nil {
		tun.connState(tun, StateStarted)
	}

	return tun.stop(<-errChan)
}

// Stop closes all connections and makes Start exit gracefuly.
func (tun *SSHTun) Stop() {
	tun.mutex.Lock()
	defer tun.mutex.Unlock()

	if tun.started {
		tun.cancel()
	}
}

func (tun *SSHTun) initSSHConfig() (*ssh.ClientConfig, error) {
	config := &ssh.ClientConfig{
		Config: ssh.Config{
			KeyExchanges: tun.sshConfigKeyExchanges,
			Ciphers:      tun.sshConfigCiphers,
			MACs:         tun.sshConfigMACs,
		},
		User: tun.user,
		HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			return nil
		},
		Timeout: tun.timeout,
	}

	authMethod, err := tun.getSSHAuthMethod()
	if err != nil {
		return nil, err
	}

	config.Auth = []ssh.AuthMethod{authMethod}

	return config, nil
}

func (tun *SSHTun) stop(err error) error {
	tun.mutex.Lock()
	tun.started = false
	tun.mutex.Unlock()
	if tun.connState != nil {
		tun.connState(tun, StateStopped)
	}
	return err
}

func (tun *SSHTun) fromEndpoint() *Endpoint {
	if tun.forwardType == Remote {
		return tun.remote
	}

	return tun.local
}

func (tun *SSHTun) toEndpoint() *Endpoint {
	if tun.forwardType == Remote {
		return tun.local
	}

	return tun.remote
}

func (tun *SSHTun) forwardFromName() string {
	if tun.forwardType == Remote {
		return "remote"
	}

	return "local"
}

func (tun *SSHTun) forwardToName() string {
	if tun.forwardType == Remote {
		return "local"
	}

	return "remote"
}

func (tun *SSHTun) listen(listener net.Listener) error {

	errGroup, groupCtx := errgroup.WithContext(tun.ctx)
	errGroup.Go(func() error {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return fmt.Errorf("%s accept %s on %s failed: %w", tun.forwardFromName(),
					tun.fromEndpoint().Type(), tun.fromEndpoint().String(), err)
			}
			errGroup.Go(func() error {
				return tun.handle(conn)
			})
		}
	})

	<-groupCtx.Done()

	listener.Close()

	err := errGroup.Wait()

	select {
	case <-tun.ctx.Done():
	default:
		return err
	}

	return nil
}

func (tun *SSHTun) handle(conn net.Conn) error {
	err := tun.addConn()
	if err != nil {
		return err
	}

	tun.forward(conn)
	tun.removeConn()

	return nil
}

func (tun *SSHTun) addConn() error {
	tun.mutex.Lock()
	defer tun.mutex.Unlock()

	if tun.forwardType == Local && tun.active == 0 {
		sshClient, err := ssh.Dial(tun.server.Type(), tun.server.String(), tun.sshConfig)
		if err != nil {
			return fmt.Errorf("ssh dial %s to %s failed: %w", tun.server.Type(), tun.server.String(), err)
		}
		tun.sshClient = sshClient
	}

	tun.active += 1

	return nil
}

func (tun *SSHTun) removeConn() {
	tun.mutex.Lock()
	defer tun.mutex.Unlock()

	tun.active -= 1

	if tun.forwardType == Local && tun.active == 0 {
		tun.sshClient.Close()
		tun.sshClient = nil
	}
}

func (tun *SSHTun) SetName(name string) {
	tun.name = name
}

func (tun *SSHTun) Name() string {
	return tun.name
}
