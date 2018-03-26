package main

import (
	"os"
	"strconv"
	"strings"
	"flag"
	"bytes"
	"log"
	"net"
	"net/mail"
	"gopkg.in/telegram-bot-api.v4"
	"github.com/spf13/viper"
	"./smtpd"
	"mime"
	"mime/multipart"
	"io"
	"io/ioutil"
	"encoding/base64"
	"fmt"
	"time"
)

var receivers map[string]string
var bot *tgbotapi.BotAPI
var debug bool

func main() {

	configFilePath := flag.String("c", "./smtp2tg.toml", "Config file location")
	//pidFilePath := flag.String("p", "/var/run/smtp2tg.pid", "Pid file location")
	flag.Parse()

	// Load & parse config
	viper.SetConfigFile(*configFilePath)
	err := viper.ReadInConfig()
	if err != nil {
		log.Fatal(err.Error())
	}

	// Logging
	logfile := viper.GetString("logging.file")
	if logfile == "" {
		log.Println("No logging.file defined in config, outputting to stdout")
	} else {
		lf, err := os.OpenFile(logfile, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0666)
		if err != nil {
			log.Fatal(err.Error())
		}
		log.SetOutput(lf)
	}

	// Debug?
	debug = viper.GetBool("logging.debug")

	receivers = viper.GetStringMapString("receivers")
	if receivers["*"] == "" {
		log.Fatal("No wildcard receiver (*) found in config.")
	}

	var token = viper.GetString("bot.token")
	if token == "" {
		log.Fatal("No bot.token defined in config")
	}

	var listen = viper.GetString("smtp.listen")
	var name = viper.GetString("smtp.name")
	if listen == "" {
		log.Fatal("No smtp.listen defined in config.")
	}
	if name == "" {
		log.Fatal("No smtp.name defined in config.")
	}

	// Initialize TG bot
	bot, err = tgbotapi.NewBotAPI(token)
	if err != nil {
		log.Fatal(err.Error())
	}
	log.Printf("Bot authorized as %s", bot.Self.UserName)

	log.Printf("Initializing smtp server on %s...", listen)
	// Initialize SMTP server
	err = smtpd.ListenAndServe(listen, mailHandler, "mail2tg", "", debug)
	if err != nil {
		log.Fatal(err.Error())
	}
}

func mailHandler(origin net.Addr, from string, to []string, data []byte) {

	from = strings.Trim(from, " ")
	to[0] = strings.Trim(to[0], " ")
	to[0] = strings.Trim(to[0], "<")
	to[0] = strings.Trim(to[0], ">")
	msg, err := mail.ReadMessage(bytes.NewReader(data))
	if err != nil {
		log.Printf("[MAIL ERROR]: %s", err.Error())
		return
	}
	subject := msg.Header.Get("Subject")
	log.Printf("Received mail host: %s from: '%s' for '%s' with subject '%s'", origin.String(), from, to[0], subject)

	body := new(bytes.Buffer)
	body.WriteString(fmt.Sprintf("*%s* (%s)\n", subject, time.Now().Format(time.RFC1123Z)))

	file := new(bytes.Buffer)

	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil {
		log.Printf("[MAIL ERROR]: %s", err.Error())
	}
	if strings.HasPrefix(mediaType, "multipart/") {
		mr := multipart.NewReader(msg.Body, params["boundary"])
		for {
			p, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			ptype := p.Header.Get("Content-Type")
			log.Printf("PTYPE: %s", ptype)
			if strings.HasPrefix(ptype, "text/html") {
				if err != nil {
					log.Printf("[MAIL ERROR]: %s", err.Error())
					continue

				}
				log.Print("Convert HTML part to markdown")
				if err != nil {
					log.Printf("Part read error: %s", err.Error())
					continue
				}
				pbytes, err := ioutil.ReadAll(p)
				if err != nil {
					log.Printf("Read part error: %s", err.Error())
					continue
				}
				pencode := p.Header.Get("Content-Transfer-Encoding")
				if pencode == "base64" {
					pbytes, err = base64.StdEncoding.DecodeString(string(pbytes))
					if err != nil {
						log.Printf("Can not decode from base64: %s", err.Error())
						continue
					}
				}
				file.Write(pbytes)
			}
		}
	} else {
		if strings.HasPrefix(mediaType, "text/html") {
			pbytes, err := ioutil.ReadAll(msg.Body)
			if err != nil {
				log.Printf("Read body error: %s", err.Error())
			}
			pencode := msg.Header.Get("Content-Transfer-Encoding")
			if pencode == "base64" {
				pbytes, err = base64.StdEncoding.DecodeString(string(pbytes))
				if err != nil {
					log.Printf("Can not decode from base64: %s", err.Error())
				}
			}
			file.Write(pbytes)
		}else {
			body.ReadFrom(msg.Body)
		}
	}
	// Find receivers and send to TG
	var tgid string
	if receivers[to[0]] != "" {
		tgid = receivers[to[0]]
	} else {
		tgid = receivers["*"]
	}

	log.Printf("Relaying message to: %v", tgid)

	i, err := strconv.ParseInt(tgid, 10, 64)
	if err != nil {
		log.Printf("[ERROR]: wrong telegram id: not int64")
		return
	}
	if file.Len() > 0 {
		fb := tgbotapi.FileBytes{Name: "report.html", Bytes: file.Bytes()}
		tgMsg := tgbotapi.NewDocumentUpload(i, fb)
		tgMsg.Caption = fmt.Sprintf("%s\n%s", subject, time.Now().Format(time.RFC1123Z))
		//log.Printf("File: %q",fb.Bytes)
		_, err = bot.Send(tgMsg)
		if err != nil {
			log.Printf("[ERROR]: Send document: %s", err.Error())
		}
	}else {
		tgMsg := tgbotapi.NewMessage(i, body.String())
		tgMsg.ParseMode = tgbotapi.ModeMarkdown
		_, err = bot.Send(tgMsg)
		if err != nil {
			log.Printf("[ERROR]: Send message: %s", err.Error())
		}
	}
}
