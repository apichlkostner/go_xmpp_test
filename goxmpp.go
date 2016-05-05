//    Test program created while learning go and xmpp
//
//    Copyright (C) 2016 Arthur Pichlkostner
//
//    This program is free software; you can redistribute it and/or modify
//    it under the terms of the GNU General Public License as published by
//    the Free Software Foundation; either version 2 of the License, or
//    (at your option) any later version.
//
//    This program is distributed in the hope that it will be useful,
//    but WITHOUT ANY WARRANTY; without even the implied warranty of
//    MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
//    GNU General Public License for more details.
//
//    You should have received a copy of the GNU General Public License along
//    with this program; if not, write to the Free Software Foundation, Inc.,
//    51 Franklin Street, Fifth Floor, Boston, MA 02110-1301 USA.

package main

import (
	"fmt"
	"net"
	"os"
	"crypto/tls"
	"strconv"
	"io"
	"bufio"
	"encoding/xml"
	"encoding/base64"
	"encoding/json"
)

type Config struct {
	Server_name string      `json:"server_name"`
	User_name   string      `json:"user_name"`
	Password    string      `json:"password"`
	Receiver    string      `json:"receiver"`
	Default_msg string      `json:"default_message"`
}
	

type Stanza struct {
	XMLName     xml.Name     `xml:"message"`
	To          string       `xml:"to,attr"`
	Message     string       `xml:"body"`
	Id          string       `xml:"id"`
}

func main() {
	// load configuration
	conf_file, err := os.Open("goxmpp_config.json")
	if err != nil {
		error_and_exit("Error reading configuration file: %v\n", err)
	}
	conf_decoder := json.NewDecoder(conf_file)
	config := Config{}
	if err := conf_decoder.Decode(&config); err != nil {
		error_and_exit("Error decoding configuration file: %v\n", err)
	}
	
	server_name := config.Server_name
	passwd := config.Password
	user_name   := config.User_name
	receiver_name := config.Receiver

	// lookup xmpp service
	name, srv, err := net.LookupSRV("xmpp-client", "tcp", server_name)
	if err != nil {
		error_and_exit("Error while lookup xmpp-client: %v", err)
	}

	fmt.Printf("Server = %v services = %v", name, srv)

	// open tcp connection
	server := net.JoinHostPort(srv[0].Target, strconv.FormatUint(uint64(srv[0].Port),10))

	conn, err := net.Dial("tcp", server)
	if err != nil {
		error_and_exit("Error connection to server"+server+" : %v\n", err)
	}

	in := xml.NewDecoder(conn)

	mystr :=  "<?xml version='1.0'?><stream:stream from='" + user_name + "@" + server_name + "' to='" + server_name + "' version='1.0' xml:lang='en' xmlns='jabber:client' xmlns:stream='http://etherx.jabber.org/streams'>"

	fmt.Printf("\nStart stream: %s\n", mystr)

	if _, err = io.WriteString(conn, mystr); err != nil {
		error_and_exit("Error starting stream: %v\n", err)
	}

	parse_response(in, "")

	info := make([]byte, 256)

	// switch to tls connection
	fmt.Printf("\nStart TLS negotiation...\n")
	_, err = io.WriteString(conn, "<starttls xmlns='urn:ietf:params:xml:ns:xmpp-tls'/>")
	n, err := conn.Read(info)
	fmt.Printf("Response: %s\n", string(info[:n]))


	conf := tls.Config{
		InsecureSkipVerify: true,
	}
	tlsconn := tls.Client(conn, &conf)
	if err = tlsconn.Handshake(); err != nil {
		error_and_exit("Error tls handshake: %v\n", err)
	}

	in_tls := xml.NewDecoder(tlsconn)
	fmt.Printf("\nResending stream...\n")
	if _, err = io.WriteString(tlsconn, mystr); err != nil {
		error_and_exit("Error sending stream after tls handshake: %v\n", err)
	}

	parse_response(in_tls, "")

	fmt.Printf("\nStart SASL\n")
	challenge := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%s\x00%s\x00%s", user_name, user_name, passwd)))
	auth_string := "<auth xmlns='urn:ietf:params:xml:ns:xmpp-sasl' mechanism='PLAIN'>" + challenge + "</auth>"

	_, err = io.WriteString(tlsconn, auth_string)

	parse_response(in_tls, "")

	fmt.Printf("\nSending new stream...\n")
	_, err = io.WriteString(tlsconn, mystr)
	stream_id := parse_response(in_tls, "id")

	ressource_string := "<iq id='" + stream_id + "' type='set'><bind xmlns='urn:ietf:params:xml:ns:xmpp-bind'/></iq>"
	fmt.Printf("\nSending ressource %s\n", ressource_string)
	if _, err = io.WriteString(tlsconn, ressource_string); err != nil {
		error_and_exit("Error requesting ressource: %v\n", err)
	}

	parse_response(in_tls, "")

	enc := xml.NewEncoder(tlsconn)

	ch := make(chan string)
	go xmpp_message_receiver(in_tls, ch)

	for {
		reader := bufio.NewReader(os.Stdin)
		fmt.Print("Enter text: ")
		text, _ := reader.ReadString('\n')
		fmt.Println("Sending "+text)
		if err := enc.Encode(Stanza{To: receiver_name, Message: text}); err != nil {
			error_and_exit("Error writing stanza: %v\n", err)
		}
	}

	tlsconn.Close()
	conn.Close()
}


func parse_response(in *xml.Decoder, search_element string) string {
	run_loop := true
	first    := true
	end_token := ""
	ret_string := ""

	for run_loop {
		tok, err := in.Token()
		if err == io.EOF {
			break
		} else if err != nil {
			fmt.Fprintf(os.Stderr, "%s\n", err)
			os.Exit(1)
		}
		switch tok := tok.(type) {
		case xml.StartElement:
			if first && (tok.Name.Local != "stream") {
				end_token = tok.Name.Local
				first = false
				//fmt.Printf("end_token = %s\n", end_token)
			}
			fmt.Printf("%s\n", tok.Name.Local)
			fmt.Println(tok.Attr)
			for _, attr := range tok.Attr {
				if attr.Name.Local == search_element {
					ret_string = attr.Value
				}
			}
		case xml.EndElement:
			if tok.Name.Local == end_token {
				run_loop = false
			}
		case xml.CharData:
			fmt.Printf("%s\n", string(tok))
		}
	}
	return ret_string
}

func error_and_exit(message string, err error) {
		fmt.Fprintf(os.Stderr, message, err)
		os.Exit(1)
}

func xmpp_message_receiver(in *xml.Decoder, ch chan<- string) {
	for {
		parse_response(in, "")
	}
}
