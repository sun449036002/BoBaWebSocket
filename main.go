package main

import (
"encoding/json"
"fmt"
"net/http"

"github.com/gorilla/websocket"
"github.com/satori/go.uuid"
	"github.com/garyburd/redigo/redis"
	"github.com/json-iterator/go"
	"talkGo/models"
)

type ClientManager struct {
	clients    map[*Client]bool
	broadcast  chan []byte
	register   chan *Client
	unregister chan *Client
}

type Client struct {
	id     string
	socket *websocket.Conn
	send   chan []byte
}

type Message struct {
	Sender    string `json:"sender,omitempty"`
	Recipient string `json:"recipient,omitempty"`
	Content   string `json:"content,omitempty"`
	Nickname   string `json:"nickname,omitempty"`
	AvatarUrl   string `json:"avatarurl,omitempty"`
}

type Content struct {
	Val string
	Sk string
}

var manager = ClientManager{
	broadcast:  make(chan []byte),
	register:   make(chan *Client),
	unregister: make(chan *Client),
	clients:    make(map[*Client]bool),
}

func (manager *ClientManager) start() {
	for {
		select {
		case conn := <-manager.register:
			manager.clients[conn] = true
			jsonMessage, _ := json.Marshal(&Message{Content: "one new person has connected."})
			manager.send(jsonMessage, conn)
		case conn := <-manager.unregister:
			if _, ok := manager.clients[conn]; ok {
				close(conn.send)
				delete(manager.clients, conn)
				jsonMessage, _ := json.Marshal(&Message{Content: "one person has disconnected."})
				manager.send(jsonMessage, conn)
			}
		case message := <-manager.broadcast:
			for conn := range manager.clients {
				select {
				case conn.send <- message:
				default:
					close(conn.send)
					delete(manager.clients, conn)
				}
			}
		}
	}
}

func (manager *ClientManager) send(message []byte, ignore *Client) {
	for conn := range manager.clients {
		if conn != ignore {
			conn.send <- message
		}
	}
}

func (c *Client) read() {
	defer func() {
		manager.unregister <- c
		c.socket.Close()
	}()

	for {
		_, message, err := c.socket.ReadMessage()
		if err != nil {
			manager.unregister <- c
			c.socket.Close()
			break
		}

		//取得用户信息
		rc, err := redis.Dial("tcp", "127.0.0.1:6379")
		if err != nil {
			fmt.Println("redis connect faild.....")
			break
		}
		jsonStr := string(message)
		jsonContent :=  &Content{}
		err = jsoniter.UnmarshalFromString(jsonStr, &jsonContent)
		if err != nil {
			fmt.Println(err.Error())
			break
		}
		fmt.Println(jsonContent.Val, jsonContent.Sk)

		userinfoJson, err := redis.String(rc.Do("get", "userinfo_" + jsonContent.Sk))
		if err != nil {
			fmt.Println("get userinfo_" + jsonContent.Sk,  err)
			break
		}

		user := models.User{}
		err = jsoniter.UnmarshalFromString(userinfoJson, &user)
		if err != nil {
			fmt.Println("userinfo json decode faild")
			break
		}

		fmt.Println("nickname", user.Username)

		jsonMessage, _ := json.Marshal(&Message{Sender: c.id, Content: jsonContent.Val, Nickname : user.Username})
		manager.broadcast <- jsonMessage
	}
}

func (c *Client) write() {
	defer func() {
		c.socket.Close()
	}()

	for {
		select {
		case message, ok := <-c.send:
			if !ok {
				c.socket.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			c.socket.WriteMessage(websocket.TextMessage, message)
		}
	}
}

func main() {
	fmt.Println("Starting application...")
	go manager.start()
	http.HandleFunc("/wss", wsPage)
	http.ListenAndServe(":12345", nil)
}

func wsPage(res http.ResponseWriter, req *http.Request) {
	conn, error := (&websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}).Upgrade(res, req, nil)

	if error != nil {
		http.NotFound(res, req)
		fmt.Println(error)
		return
	}

	bts := make([]byte, 0)
	lens, _ := req.Body.Read(bts)
	fmt.Println("request" , lens, bts, req.Host, req.Method)

	fmt.Println("url ", req.URL.String())
	fmt.Println("ContentLength ", req.ContentLength)
	fmt.Println("Header ", req.Header)
	fmt.Println("TransferEncoding ", req.TransferEncoding)
	fmt.Println("form ", req.Form.Encode())
	fmt.Println("post form ", req.PostForm.Encode())

	uid, _:= uuid.NewV4()
	client := &Client{id: uid.String(), socket: conn, send: make(chan []byte)}

	manager.register <- client

	go client.read()
	go client.write()
	//fmt.Println("server start ok")
}