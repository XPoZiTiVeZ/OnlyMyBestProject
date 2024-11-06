package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/coder/websocket"
)

var mut sync.Mutex

var Id 	  int
var Tasks []string
var Commands map[string]func(*Server, Client, Message)

type Message struct {
    Sender   int    `json:"sender"`
    Receiver int    `json:"receiver"`
    Message  []byte `json:"message"`
    End      bool   `json:"end"`
}

func NewMessage(sender, receiver int, message []byte, end bool) (Message, error) {
    return Message{
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
    Id           int    `json:"Id"`
    SecretKey    string `json:"SecretKey"`
    FriendlyName string `json:"FriendlyName"`
    ComputerName string `json:"ComputerName"`
    UserName     string `json:"UserName"`
    LocalIP      string `json:"LocalIP"`
    PublicIP     string `json:"PublicIP"`
    OS           string `json:"OS"`
    PingError 	 error
    ReceiveError error
    Role         string
    Conn         *websocket.Conn
}

func (client *Client) Ping() {
    var err error
    for {
        err = client.Conn.Ping(context.Background())
        if err != nil {
            client.PingError = err
            return
        }
    }
}

type Server struct {
    clients   map[int]Client
    admins    map[string]string
}

func NewServer() (Server) {
    server := Server{}

    server.clients   = map[int]Client{}
    server.admins    = map[string]string{}
    
    return server
}

func (server *Server) NewClient(secretKey, computerName, localIP, publicIP, os string, conn *websocket.Conn) (Client, error) {
    role := "client"
    if _, ok := server.admins[secretKey]; ok { role = "admin" }
    
    client := Client{
        Id:           Id,
        SecretKey:    secretKey,
        FriendlyName: "",
        ComputerName: computerName,
        LocalIP:      localIP,
        PublicIP:     publicIP,
        OS:           os,
        PingError:    nil,
        ReceiveError: nil,
        Role:         role,
        Conn:         conn,
    }

    mut.Lock()
    server.clients[Id] = client
    mut.Unlock()

    Id += 1

    return client, nil
}

func (server *Server) NewClientFromJSON(bytes []byte, conn *websocket.Conn) (Client, error) {
    var client Client
    err := json.Unmarshal(bytes, &client)
    if err != nil { return Client{}, nil }
    
    Role := "client"
    if _, ok := server.admins[client.SecretKey]; ok { Role = "admin" }
    
    client.Id           = Id
    client.PingError    = nil
    client.ReceiveError = nil    
    client.Role         = Role
    client.Conn         = conn

    mut.Lock()
    server.clients[Id] = client
    mut.Unlock()
    
    Id += 1

    return client, nil
}

func (server *Server) GetClient(id int) (Client, bool) {
    mut.Lock()
    client, ok := server.clients[id]
    mut.Unlock()

    return client, ok
}

func (server *Server) DeleteClient(id int) {
    mut.Lock()
    delete(server.clients, id)
    mut.Unlock()
}

func (server *Server) Connect(w http.ResponseWriter, r *http.Request) {
    conn, err := websocket.Accept(w, r, nil)
    if err != nil { log.Print(err, 1) }
    defer conn.CloseNow()

    _, bytes, err := conn.Read(context.Background())
    if err != nil { log.Print(err, 2) }
    
    msg, err := NewMessageFromJSON(bytes)
    if err != nil { return }

    client, err := server.NewClientFromJSON(msg.Message, conn)
    if err != nil { log.Print(err, 3) }

    err = server.Write(&client, -1, client.Id, []byte{}, true)
    if err != nil { log.Print(err, 16) }

    go client.Ping()
    for {
        msg, err := server.Read(&client)
        if client.PingError != nil { server.DeleteClient(client.Id); break }
        if err != nil { log.Print(err, 4) }

        sender, ok := server.GetClient(msg.Sender)
        if !ok || sender.Id != client.Id {
            err := server.Write(&client, -1, client.Id, []byte("неправильное значение поля sender"), true)
            if err != nil { log.Print(err, 5) }

            continue
        }

        _, ok = server.GetClient(msg.Receiver)
        if !ok && msg.Receiver != -1 {          
            err := server.Write(&client, -1, client.Id, []byte("неправильное значение поля receiver"), true)
            if err != nil { log.Print(err, 6) }

            continue
        }
        
        function, ok := Commands[strings.Split(string(msg.Message), " ")[0]]
        if !ok {
            err := server.Write(&client, -1, client.Id, []byte("команда не найдена"), true)
            if err != nil { log.Print(err, 12) }

            continue
        }

        function(server, client, msg)
    }

    client.Conn.Close(websocket.StatusNormalClosure, "")
}

func (server *Server) Read(client *Client) (Message, error) {
    _, bytes, err := client.Conn.Read(context.Background())
    if err != nil { log.Printf("Клиент с id %v и именем %v отключён", client.Id, client.ComputerName); client.PingError = err; return Message{}, err }
    
    msg, err := NewMessageFromJSON(bytes)
    if err != nil { log.Print(err, 14); return Message{}, err }

    return msg, nil
}

func (server *Server) Write(client *Client, sender, receiver int, bytes []byte, end bool) error {
    msg_bytes, err := NewMessageToJSON(sender, receiver, bytes, end)
    if err != nil { log.Print(err, 9); return err }

    err = client.Conn.Write(context.Background(), websocket.MessageText, msg_bytes)
    if err != nil { log.Print(err, 15); return err }

    return nil
}

func (server *Server) Start() {
    http.HandleFunc("/ws/", server.Connect)
    // http.HandleFunc("/web/", nil)

    http.ListenAndServe("127.0.0.1:8080", nil)
}

func ListUsersFunction(server *Server, client Client, msg Message) {
    clients := []Client{}
    for _, client_ := range server.clients {
        clients = append(clients, client_)
    }
    
    bytes, err := json.Marshal(clients)
    if err != nil {
        err := server.Write(&client, -1, msg.Sender, []byte("ошибка сбора пользователей"), true)
        if err != nil { log.Print(err, 13) }
        return
    }

    err = server.Write(&client, -1, msg.Sender, bytes, true)
    if err != nil { log.Print(err, 14) }
}

func ExecuteFunction(server *Server, client Client, msg Message) {
    cmdparts := strings.Split(string(msg.Message), " ")
    if len(cmdparts) < 4 { server.Write(&client, -1, client.Id, []byte("слишком короткая команда"), true); return }

    executerIDstr, cmd := cmdparts[1], strings.Join(cmdparts[3:], "")
    var executerID int
    if n, _ := fmt.Sscan(executerIDstr, &executerID); n == 0 { server.Write(&client, -1, client.Id, []byte("ID не число"), true); return }

    executer, ok := server.GetClient(executerID)
    if !ok { server.Write(&client, -1, client.Id, []byte("ID пользователя не найден"), true); return }

    if executer.Id == client.Id { server.Write(&client, -1, client.Id, []byte("нельзя использовать комамнду на себе"), true); return }

    if executer.Role != "client" { server.Write(&client, -1, client.Id, []byte("не клиент не может выполнять команды"), true); return }

    server.Write(&executer, client.Id, executer.Id, []byte(cmd), true)

    for {
        msg_exec, err := server.Read(&executer)
        if err != nil { log.Print(err, 17) }

        err = server.Write(&client, executer.Id, client.Id, msg.Message, msg_exec.End)
        if err != nil { log.Print(err, 18) }

        msg, err = server.Read(&client)
        if err != nil { log.Print(19) }
        
        err = server.Write(&executer, executer.Id, client.Id, msg.Message, msg.End)
        if err != nil { log.Print(20) }

        if msg_exec.End { break }
    }
}

func main() {
    if len(os.Args) < 2 { log.Print("USAGE ERROR\nUSAGE:\n./server <file_name>", 10) }
    
    Commands = map[string]func(*Server, Client, Message){}
    Commands["#!help"]       = nil
    Commands["#!list_users"] = ListUsersFunction
    Commands["#!execute"]    = ExecuteFunction
    
    server := NewServer()
    
    admin_bytes, err := os.ReadFile(os.Args[1])
    if err != nil { log.Print(err, 11) }
    
    admins := strings.Split(string(admin_bytes), ";")
    for _, admin := range admins { server.admins[admin] = "" }

    server.Start()
}
