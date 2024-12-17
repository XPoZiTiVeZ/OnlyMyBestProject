package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/user"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
)

var mut sync.Mutex

var PingError         error
var ReceiveError      error
var Commands          map[string]func(*Client, string)
var CommandsServer    map[string]func(*Client, Message)
var CommandsExecution map[int]*bufio.Writer
var Next = true

type Message struct {
    Method   string `json:"Method"`
    Sender   int    `json:"sender"`
    Receiver int    `json:"receiver"`
    Message  []byte `json:"message"`
    End      bool   `json:"end"`
}

func NewMessage(method string, sender, receiver int, message []byte, end bool) (Message, error) {
    return Message{
        Method:   method,
        Sender:   sender,
        Receiver: receiver,
        Message:  message,
        End:      end,
    }, nil
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

func Serialize(data []byte) (message Message, err error) {
    err = json.Unmarshal(data, &message)
    return
}
type Client struct {
    Id           int	`json:"Id"`
    SecretKey    string `json:"SecretKey"`
    FriendlyName string `json:"FriendlyName"`
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

    err = client.Write("#!handshake", client.Id, -1, bytes, true)
    if err != nil { return err }

    msg, err := client.Read()
    if err != nil { return err }
    client.Id = msg.Receiver

    // go client.Ping()
    go client.Receive()
    client.Send()
    if PingError != nil {
        log.Print("Отключён от сервера")
    }

    conn.Close(websocket.StatusNormalClosure, "")
    return nil
}

func (client *Client) Send() {
    for {
        for !Next { time.Sleep(50 * time.Millisecond) }

        fmt.Print(">>> ")
        reader := bufio.NewReader(os.Stdin)
        bytes, _, _ := reader.ReadLine()
        
        function, ok := Commands[strings.Split(string(bytes), " ")[0]]
        if !ok { log.Print("команда не найдена"); continue }

        Next = false
        if function == nil {
            fmt.Println("Комманда недоступна в данное время.")
            Next = true
            continue
        }
        function(client, string(bytes))
    }
}

func (client *Client) Receive() {
    for {
        msg, err := client.Read()
        if err != nil { log.Print(err, 1) }
    
        function, ok := CommandsServer[msg.Method]
        if !ok { log.Panic(string(msg.Message), msg.Method) }
        
        function(client, msg)
    }
}

func ErrorFunctionServer(client *Client, msg Message) {
    fmt.Println("Ошибка", string(msg.Message))
    Next = true
}

func ListUsersFunction(client *Client, cmd string) {
    cmdparts := strings.Split(cmd, " ")
    err := client.Write("#!list_users", client.Id, -1, []byte(strings.Join(cmdparts[1:], " ")), true)
    if err != nil { log.Print(err, 14) }
}

func ListUsersFunctionServer(client *Client, msg Message) {
    var clients []Client
    err := json.Unmarshal(msg.Message, &clients)
    if err != nil { log.Print(string(msg.Message), 2) }

    tw := table.NewWriter()
    tw.AppendHeader(table.Row{"#", "FriendlyName", "ComputerName", "UserName", "LocalIP", "PublicIP", "OS"})
    for _, client_ := range clients {
        friendlyName := client_.FriendlyName
        if client.Id == client_.Id { friendlyName = "You" }
        tw.AppendRow(table.Row{
            client_.Id,
            friendlyName,
            client_.ComputerName,
            client_.UserName,
            client_.LocalIP, 
            client_.PublicIP,
            client_.OS,
        })
    }

    tw.SortBy([]table.SortBy{{Name: "#", Mode: table.Asc}})
    tw.Style().Format.Header = text.FormatTitle
    tw.Style().Options.SeparateColumns = false
    
    fmt.Println(tw.Render())
    Next = true
}

func SupportFunction(client *Client, cmd string) {
    cmdparts := strings.Split(cmd, " ")
    err := client.Write("#!support", client.Id, -1, []byte(strings.Join(cmdparts[1:], " ")), true)
    if err != nil { log.Panic(err) }
}

func SupportFunctionServer(client *Client, msg Message) {
    var cmds [][]string
    err := json.Unmarshal(msg.Message, &cmds)
    if err != nil { log.Panic(err, string(msg.Message)) }
    
    for _, cmd := range cmds {
        fmt.Println(cmd[0])
        fmt.Println(cmd[1])
        for _, line := range strings.Split(cmd[2], "\n") {
            fmt.Println(line)
        }
        fmt.Println()
    }
    Next = true
}

func ExecuteFunction(client *Client, cmd string) {
    cmdparts := strings.Split(cmd, " ")
    if len(cmdparts) < 3 { log.Print("слишком короткая команда"); Next = true; return }

    var receiverID int
    if n, _ := fmt.Sscan(cmdparts[1], &receiverID); n == 0 { log.Print("ID не число"); Next = true; return }

    err := client.Write("#!execute", client.Id, receiverID, []byte(strings.Join(cmdparts[1:], " ")), true)
    if err != nil { log.Panic(err); return }
}

func ExecuteFunctionServer(client *Client, msg Message) {
    msg, err := client.Read()
    if err != nil { log.Panic(err) }
    cmdparts := strings.Split(string(msg.Message), " ")

    Next = true

    var cmdid int
    if n, _ := fmt.Scan(cmdparts[1], &cmdid); n == 0 { log.Panic(cmdparts)}

    mut.Lock()
    if cmdparts[0] == "stdout" {
        CommandsExecution[cmdid] = bufio.NewWriter(os.Stdout)
    } else {
        file, _  := os.OpenFile(cmdparts[0], os.O_CREATE|os.O_APPEND, 0777)
        CommandsExecution[cmdid] = bufio.NewWriter(file)
    }
    mut.Unlock()
}

func ExecutionFunctionServer(client *Client, msg Message) {
    cmdparts := strings.Split(string(msg.Message), " ")
    var cmdid int
    if n, _ := fmt.Sscan(cmdparts[1], &cmdid); n == 0 { log.Panic(cmdparts) }

    mut.Lock()
    stdout := CommandsExecution[cmdid]
    mut.Unlock()

    if !msg.End {
        msgparts := strings.Split(string(msg.Message), " ")
        stdout.Write([]byte(strings.Join(msgparts[2:], " ")))
        stdout.Flush()
        client.Write("#!execution", client.Id, msg.Sender, []byte("ok"), true)
    } else if cmdparts[2] == "" {
        stdout.Write([]byte(fmt.Sprintf("> Command %d executed\n", cmdid)))
        stdout.Flush()
        fmt.Printf("> Command %d executed\n", cmdid)
    } else if cmdparts[2] == "start" {
        mut.Lock()
        if cmdparts[3] == "stdout" {
            CommandsExecution[cmdid] = bufio.NewWriter(os.Stdout)
        } else {
            file, _  := os.OpenFile(cmdparts[3], os.O_CREATE|os.O_APPEND, 0777)
            CommandsExecution[cmdid] = bufio.NewWriter(file)
        }
        mut.Unlock()
        client.Write("#!execution", client.Id, msg.Sender, []byte("ok"), true)
    } else if cmdparts[2] == "log" {
        os.WriteFile("log.txt", []byte(cmdparts[3]), 0o644)
    } else if cmdparts[2] == "deleted" {
        fmt.Printf("> Info about command %d deleted\n", cmdid)
    } else if cmdparts[2] == "interrupted" {
        stdout.Write([]byte(fmt.Sprintf("> Interrupted %d\n", cmdid)))
        stdout.Flush()
        fmt.Printf("> Interrupted %d\n", cmdid)
    }
}

func (client *Client) Read() (Message, error) {
    _, bytes, err := client.Conn.Read(context.Background())
    if err != nil { log.Print(err, 3); return Message{}, err }
    
    msg, err := NewMessageFromJSON(bytes)
    if err != nil { log.Print(err, 14); return Message{}, err }

    fmt.Println("Read:", msg.Method, string(msg.Message), msg.End)
    return msg, nil
}

func(client *Client) Write(method string, sender, receiver int, bytes []byte, end bool) error {
    fmt.Println("Write:", method, string(bytes), end)
    msg_bytes, err := NewMessageToJSON(method, sender, receiver, bytes, end)
    if err != nil { log.Print(err, 4); return err }

    err = client.Conn.Write(context.Background(), websocket.MessageText, msg_bytes)
    if err != nil { log.Print(err, 10); return err }

    return nil
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
    CommandsExecution = make(map[int]*bufio.Writer)

    Commands = map[string]func(*Client, string){}
    Commands["#!help"]       = nil
    Commands["#!support"]    = SupportFunction
    Commands["#!list_users"] = ListUsersFunction
    Commands["#!execute"]    = ExecuteFunction
    // Commands["#!execution"]  = ExecutionFunction

    CommandsServer = map[string]func(*Client, Message){}
    CommandsServer["#!help"]       = nil
    CommandsServer["#!error"]      = ErrorFunctionServer
    CommandsServer["#!support"]    = SupportFunctionServer
    CommandsServer["#!list_users"] = ListUsersFunctionServer
    CommandsServer["#!execute"]    = ExecuteFunctionServer
    CommandsServer["#!execution"]  = ExecutionFunctionServer

    secretKey := "44d927f49f08355c424b01da13a4913a2a59b50c3f353864844621f881c4d1bc"

    computerName, err := getComputerName()
    if err != nil { log.Print("Warning: ", err) }

    userName, err := getUserName()
    if err != nil { log.Print("Warning: ", err) }

    publicIP, err := getPublicIP()
    if err != nil { log.Print("Warning: ", err) }

    localIP, err := getLocalIP()
    if err != nil { log.Print("Warning: ", err) }

    client := NewClient(secretKey, computerName, userName, localIP, publicIP, runtime.GOOS)
    client.Start("ws://127.0.0.1:8080/ws/")
}
