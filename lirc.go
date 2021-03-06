package lirc

import (
	"bufio"
	"encoding/hex"
	"errors"
	"log"
	"net"
	"strconv"
	"strings"
	"time"
)

// Router manages sending and receiving of commands / data
type Router struct {
	handlers map[remoteButton]Handle

	path       string
	host       string
	connection net.Conn
	writer     *bufio.Writer
	reply      chan Reply
	receive    chan Event
	running    bool
}

// Event represents the IR Remote Key Press Event
type Event struct {
	Code   uint64
	Repeat int64
	Button string
	Remote string
}

// Reply received when a command is sent
type Reply struct {
	Command    string
	Success    int
	DataLength int
	Data       []string
}

// Init initializes the connection to lirc daemon
func Init(path string) (*Router, error) {
	l := new(Router)

	c, err := net.Dial("unix", path)

	if err != nil {
		return nil, err
	}

	l.connection = c

	l.path = path

	l.writer = bufio.NewWriter(c)
	l.reply = make(chan Reply)
	l.receive = make(chan Event)

	go reader(l)

	return l, nil
}

func InitTCP(host string) (*Router, error) {
	l := new(Router)

	c, err := net.Dial("tcp", host)

	if err != nil {
		return nil, err
	}

	l.connection = c

	l.host = host

	l.writer = bufio.NewWriter(c)
	l.reply = make(chan Reply)
	l.receive = make(chan Event)

	go reader(l)

	return l, nil
}

func reader(router *Router) {
	const (
		RECEIVE = iota
		REPLY
		MESSAGE
		STATUS
		DATA_START
		DATA_LEN
		DATA
		END
	)

	var message Reply
	state := RECEIVE
	dataCnt := 0
	scanner := bufio.NewScanner(router.connection)
	for scanner.Scan() {
		line := scanner.Text()

		switch state {
		case RECEIVE:
			if line == "BEGIN" {
				state = REPLY
			} else {
				r := strings.Split(line, " ")
				c, err := hex.DecodeString(r[0])
				if err != nil {
					log.Println("Invalid lirc broadcats message received - code not parseable")
					continue
				}
				if len(c) != 8 {
					log.Println("Invalid lirc broadcats message received - code has wrong length")
					continue
				}

				var code uint64
				code = 0
				for i := 0; i < 8; i++ {
					code &= uint64(c[i]) << uint(8*i)
				}

				var event Event
				event.Repeat, err = strconv.ParseInt(r[1], 16, 0)
				if err != nil {
					log.Println("Invalid lirc broadcats message received - invalid repeat count")
				}
				event.Code = code
				event.Button = r[2]
				event.Remote = r[3]
				router.receive <- event
			}
		case REPLY:
			message.Command = line
			message.Success = 0
			message.DataLength = 0
			message.Data = message.Data[:0]
			state = STATUS
		case STATUS:
			if line == "SUCCESS" {
				message.Success = 1
				state = DATA_START
			} else if line == "END" {
				message.Success = 1
				state = RECEIVE
				router.reply <- message
			} else if line == "ERROR" {
				message.Success = 0
				state = DATA_START
			} else {
				log.Println("Invalid lirc reply message received - invalid status")
				state = RECEIVE
			}
		case DATA_START:
			if line == "END" {
				state = RECEIVE
				router.reply <- message
			} else if line == "DATA" {
				state = DATA_LEN
			} else {
				log.Println("Invalid lirc reply message received - invalid data start")
				state = RECEIVE
			}
		case DATA_LEN:
			dataCnt = 0
			var err error
			message.DataLength, err = strconv.Atoi(line)
			if err != nil {
				log.Println("Invalid lirc reply message received - invalid data len")
				state = RECEIVE
			} else {
				state = DATA
			}
		case DATA:
			if dataCnt < message.DataLength {
				message.Data = append(message.Data, line)
			}
			dataCnt++
			if dataCnt == message.DataLength {
				state = END
			}
		case END:
			state = RECEIVE
			if line == "END" {
				router.reply <- message
			} else {
				log.Println("Invalid lirc reply message received - invalid end")
			}
		}
	}
	if err := scanner.Err(); err != nil {
		// only log error if the router is still in running state
		if router.running {
			log.Println("error reading from lircd socket")
		}
		router.Close()
	} else {
		log.Println("lircd connection error")
		router.Close()
	}
}

// Command - Send any command to lircd
func (l *Router) Command(command string) Reply {
	l.writer.WriteString(command + "\n")
	l.writer.Flush()

	reply := <-l.reply

	return reply
}

// Send a SEND_ONCE command
func (l *Router) Send(command string) error {
	reply := l.Command("SEND_ONCE " + command)
	if reply.Success == 0 {
		return errors.New(strings.Join(reply.Data, " "))
	}
	return nil
}

// SendLong sends a SEND_START command followed by a delay and SEND_STOP`
func (l *Router) SendLong(command string, delay time.Duration) error {
	reply := l.Command("SEND_START " + command)
	if reply.Success == 0 {
		return errors.New(strings.Join(reply.Data, " "))
	}
	time.Sleep(delay)
	reply = l.Command("SEND_STOP " + command)
	if reply.Success == 0 {
		return errors.New(strings.Join(reply.Data, " "))
	}

	return nil
}

// Close the connection to lirc daemon
func (l *Router) Close() {
	l.running = false
	l.connection.Close()
	close(l.receive)
}
