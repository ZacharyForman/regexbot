package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"
)

type Message struct {
	nick, content string
}

type Buffer struct {
	messages []Message
	sync.Mutex
}

func (b *Buffer) prepend(msg Message) {
	v := make([]Message, 0)
	if len(msg.content) > 511 {
		msg.content = msg.content[0:510]
	}
	v = append(v, msg)
	b.messages = append(v, b.messages...)
	if len(b.messages) > 100 {
		b.messages = b.messages[0:100]
	}
}

func splitRegex(s string) (string, string) {
	var pos int
	var escaped bool
	var slashes []int
	//find section 1
	for pos = 0; pos < len(s); pos++ {
		if s[pos] == '\\' && !escaped {
			s = s[:pos-1] + s[pos:]
			escaped = true
		} else if s[pos] == '\\' && escaped {
			s = s[:pos] + "\\" +  s[pos:]
		} else {
			escaped = false
		}
		if s[pos] == '/' && !escaped {
			slashes = append(slashes, pos)
		}
	}
	if s[len(s)-1] != '/' {
		s = s + "/"
		slashes = append(slashes, len(s)-1)
	}
	if len(slashes) < 2 {
		return "", ""
	}
	fmt.Println(s[slashes[0]+1:slashes[1]], s[slashes[1]+1:slashes[len(slashes)-1]])
	return s[slashes[0]+1 : slashes[1]], s[slashes[1]+1 : slashes[len(slashes)-1]]
}

func (b *Buffer) TranslationFindMatch(trans map[rune]rune) (int, Message) {
	for pos, v := range b.messages {
		for k, _ := range trans {
			if strings.ContainsRune(v.content, k) {
				return pos, v
			}
		}
	}
	return -1, Message{"", ""}
}

func (b *Buffer) SubstitutionFindMatch(re *regexp.Regexp) (int, Message) {
	for pos, v := range b.messages {
		if re.MatchString(v.content) {
			return pos, v
		}
	}
	return -1, Message{"", ""}
}

func (b *Buffer) TranslationRegex(m Message) Message {
	translation := make(map[rune]rune)
	str1, str2 := splitRegex(m.content)
	for len(str1) > 0 && len(str2) > 0 {
		r1, size1 := utf8.DecodeRuneInString(str1)
		r2, size2 := utf8.DecodeRuneInString(str2)
		translation[r1] = r2
		str1 = str1[size1:]
		str2 = str2[size2:]
	}
	if len(str1) != len(str2) {
		return Message{m.nick, "Translations have different lengths"}
	}
	pos, msg := b.TranslationFindMatch(translation)
	if pos == -1 {
		return Message{m.nick, "No match found."}
	}
	output := make([]rune, 0, len(msg.content))
	for len(msg.content) > 0 {
		r, size := utf8.DecodeRuneInString(msg.content)
		if t, ok := translation[r]; ok {
			output = append(output, t)
		} else {
			output = append(output, r)
		}
		msg.content = msg.content[size:]
	}
	b.messages = append(b.messages[:pos], b.messages[pos+1:]...)
	b.prepend(Message{msg.nick, string(output)})
	return Message{msg.nick, string(output)}
}

func (b *Buffer) SubstitutionRegex(m Message) Message {
	if strings.Count(m.content, "/") < 2 {
		return Message{m.nick, "Too few '/'s."}
	}
	pattern, replacement := splitRegex(m.content)
	re, err := regexp.Compile(pattern)
	if err != nil {
		return Message{m.nick, fmt.Sprintf("%v", err)}
	}
	pos, msg := b.SubstitutionFindMatch(re)
	if pos == -1 {
		return Message{m.nick, "No match found."}
	}
	output := re.ReplaceAllString(msg.content, replacement)
	b.messages = append(b.messages[:pos], b.messages[pos+1:]...)
	b.prepend(Message{msg.nick, output})
	return Message{msg.nick, output}
}

//end regex shit

func SendToServer(conn net.Conn, msg string) {
	if len(msg) > 511 {
		msg = msg[0:510]
	}
	fmt.Fprintf(conn, "%s\n", strings.Replace(strings.Replace(msg, "\n", "", -1), "\r", "", -1))
}

func output(conn net.Conn, ch, msg string) {
	SendToServer(conn, fmt.Sprintf("PRIVMSG %s :%s", ch, msg))
}

func (b *Buffer) handleMessage(conn net.Conn, channel string, m Message) {
	b.Lock()
	defer b.Unlock()
	switch {
	case strings.HasPrefix(m.content, "s/"):
		res := b.SubstitutionRegex(m)
		output(conn, channel, fmt.Sprintf("<%s> %s", res.nick, res.content))
	case strings.HasPrefix(m.content, "y/"):
		res := b.TranslationRegex(m)
		output(conn, channel, fmt.Sprintf("<%s> %s", res.nick, res.content))
	default:
		b.prepend(m)
	}
}

func HandleChannel(conn net.Conn, channel string, messages chan Message) {
	var b Buffer
	b.messages = make([]Message, 0)
	for {
		msg := <-messages
		go b.handleMessage(conn, channel, msg)
	}
}

func parseStatus(status string) (s string, msg Message) {
	strs := strings.SplitN(status, " ", 4)
	if strs[1] != "PRIVMSG" {
		fmt.Printf("Ignoring message: %s\n", status)
		return "", Message{"", ""}
	}
	msg.nick = strings.Split(strs[0][1:], "!")[0]
	msg.content = strs[3][1:]
	return strs[2], msg
}

func TerminalControl(conn net.Conn) {
	reader := bufio.NewReader(os.Stdin)
	for cmd, err := reader.ReadString('\n'); err == nil; cmd, err = reader.ReadString('\n') {
		fmt.Fprintf(conn, "%s", cmd)
	}
}

func main() {
	conn, err := net.Dial("tcp", "irc.freenode.net:6667")
	if err != nil {
		fmt.Println(err)
		return
	}
	//connect to the server
	fmt.Fprintf(conn, "USER "+"asregexbot"+" 8 * :"+"asregexbot"+"\n")
	fmt.Fprintf(conn, "NICK "+"asregexbot"+"\n")
	fmt.Fprintf(conn, "JOIN #vnc-test\n")
	go TerminalControl(conn)
	reader := bufio.NewReader(conn)
	chanchans := make(map[string]chan Message)
	for {
		status, err := reader.ReadString('\n')
		if err != nil {
			fmt.Print(err)
			return
		}
		if strings.HasPrefix(status, "PING") {
			fmt.Fprintf(conn, "PONG %s\n", status[4:])
			continue
		}
		status = status[:len(status)-2]
		//handle multiple channels
		channel, msg := parseStatus(status)
		if channel != "" {
			//if we're not ignoring the message:
			if chanchans[channel] == nil {
				//spin up a new goroutine.
				chanchans[channel] = make(chan Message, 100)
				chanchans[channel] <- msg
				go HandleChannel(conn, channel, chanchans[channel])
				fmt.Printf("%s: <%s> %s\n", channel, msg.nick, msg.content)
			} else {
				chanchans[channel] <- msg
				fmt.Printf("%s: <%s> %s\n", channel, msg.nick, msg.content)
			}
		}
	}
}
