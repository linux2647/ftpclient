package main

import (
	"fmt"
	"io/ioutil"
	"net"
	"net/textproto"
	"regexp"
	"strconv"
	"strings"
)

// FTP Statuses
const (
	noStatus                    = -1
	statusSendData              = 150
	statusSuccess               = 200
	statusInformative           = 211
	statusHelp                  = 214
	statusEnterPassiveMode      = 221
	statusDataConnectionClosing = 221
	statusTransferComplete      = 226
	statusLoggedIn              = 230
	statusDeleteSuccess         = 250
	statusDirectoryChange       = 250
	statusDirectorySuccess      = 257
	statusRequiresPassword      = 331
)

// FtpClient is a client with which can talk to FTP servers.
type FtpClient struct {
	connection *textproto.Conn
}

// FtpMode specifies if a connection should transfer data in ASCII, "text",
// mode or binary mode.
type FtpMode string

const (
	// ASCII will transfer data so that newlines will conform to the client's
	// OS preference.  This is preferable for text.
	ASCII FtpMode = "A"
	// BINARY will transfer data without newline conversion.  This is
	// preferable for anything but text.
	BINARY FtpMode = "I"
)

// passiveData is used for communicating between the main routine and
// goroutines that handle passive FTP data connections.
type passiveData struct {
	// TODO data should be []byte instead of string
	data string
	err  error
}

// Connect establishes an FTP connection to a server and returns an FtpClient.
// Host can be in the form of "host" or "host:port".  Host can be either a
// hostname or an IP address.
func Connect(host, username, password string) (*FtpClient, string, error) {
	// Establish a connection
	connection, err := textproto.Dial("tcp", host)
	if err != nil {
		return nil, "", err
	}

	// Get the hello from the server
	client := &FtpClient{connection}
	code, message, err := client.helloFromServer()
	if err != nil {
		return nil, "", err
	}

	// Authenticate with a username (may return early if password isn't needed)
	code, err = client.User(username)
	if err != nil && code != statusLoggedIn {
		return nil, message, err
	}

	// Authenticate with a password
	code, err = client.Password(password)
	if err != nil {
		message = fmt.Sprintf("%d %s", code, message)
		return nil, message, err
	}

	return client, message, nil
}

// helloFromServer gets the initial FTP handshake message
func (c *FtpClient) helloFromServer() (int, string, error) {
	code, message, err := c.connection.ReadResponse(noStatus)
	return code, message, err
}

// expectResponse sends a command and then expects a particular FTP status code
// It returns the actual status code along with the message from the server.
// This is similar behavior to net/textproto.Connection.ReadResponse.
func (c *FtpClient) expectResponse(command string, expectCode int) (int, string, error) {
	// Send the command
	id, err := c.connection.Cmd(command)
	if err != nil {
		return noStatus, "", err
	}

	c.connection.StartResponse(id)
	defer c.connection.EndResponse(id)

	// Read what the server sent
	code, line, err := c.connection.ReadResponse(expectCode)
	return code, line, nil
}

// User authenticates an FtpClient with a particular username.
func (c *FtpClient) User(username string) (int, error) {
	command := fmt.Sprintf("USER %s", username)
	code, _, err := c.expectResponse(command, statusLoggedIn)
	return code, err
}

// Password authenticates an FtpClient with a password.  This must be preceeded
// by the User method.
func (c *FtpClient) Password(password string) (int, error) {
	command := fmt.Sprintf("PASS %s", password)
	code, _, err := c.expectResponse(command, statusRequiresPassword)
	return code, err
}

// Help retrieves the FTP commands the server understands.
func (c *FtpClient) Help() (string, error) {
	_, message, err := c.expectResponse("HELP", statusHelp)
	return message, err
}

// Stat retrieves the status of the FTP server.
func (c *FtpClient) Stat() (string, error) {
	_, message, err := c.expectResponse("STAT", statusInformative)
	return message, err
}

// Mode sets the particular data transfer mode, usually ASCII or BINARY.
func (c *FtpClient) Mode(mode FtpMode) (string, error) {
	command := fmt.Sprintf("TYPE %s", mode)
	_, message, err := c.expectResponse(command, statusSuccess)
	return message, err
}

// List retrieves the contents of the current remote directory.
func (c *FtpClient) List() (string, error) {
	// List requires a data connection
	host, err := c.passiveMode()
	if err != nil {
		return "", err
	}

	data := make(chan passiveData)
	// Start the data connection
	go passiveRead(host, data)

	// Ensure the connection is successful
	if message := <-data; message.err != nil {
		close(data)
		return "", err
	}

	// Ask for the listing
	command := fmt.Sprintf("LIST")
	_, _, err = c.expectResponse(command, statusEnterPassiveMode)
	if err != nil {
		// Something went wrong; abort the data connection
		data <- passiveData{"", err}
		return "", err
	}

	// Tell the data connection it is clear to receive
	data <- passiveData{"", nil}

	// Get the listing
	message := <-data
	if _, _, err := c.connection.ReadResponse(statusTransferComplete); err != nil {
		return "", err
	}

	return message.data, nil
}

// Retrieve gets a remote file from the server.
func (c *FtpClient) Retrieve(filename string) (string, error) {
	// Retrieve requires passive data connection
	host, err := c.passiveMode()
	if err != nil {
		return "", err
	}

	data := make(chan passiveData)
	// Start the data connection
	go passiveRead(host, data)

	// Ensure the connection is successful
	if message := <-data; message.err != nil {
		close(data)
		return "", err
	}

	// Ask for the file
	command := fmt.Sprintf("RETR %s", filename)
	_, _, err = c.expectResponse(command, statusEnterPassiveMode)
	if err != nil {
		// Something went wrong; abort the data connection
		data <- passiveData{"", err}
		return "", err
	}

	// Tell the data connection it is clear to receive
	data <- passiveData{"", nil}

	// Get the file
	message := <-data
	if _, _, err := c.connection.ReadResponse(statusTransferComplete); err != nil {
		return "", err
	}

	return message.data, nil
}

// Store sends a file to be stored on the FTP server.
func (c *FtpClient) Store(name string, contents []byte) (string, error) {
	// Store requires passive data connection
	host, err := c.passiveMode()
	if err != nil {
		return "", err
	}

	data := make(chan passiveData)
	// Start the data connection
	go passiveWrite(host, data, contents)

	// Ensure the connection is successful
	if message := <-data; message.err != nil {
		close(data)
		return "", err
	}

	// Tell the server to prepare to receive a file
	command := fmt.Sprintf("STOR %s", name)
	_, _, err = c.expectResponse(command, statusEnterPassiveMode)
	if err != nil {
		// Something went wrong; abort the data connection
		data <- passiveData{"", err}
		return "", err
	}

	// Tell the data connection it is clear to send
	data <- passiveData{"", nil}

	// Send the file
	message := <-data
	if _, _, err := c.connection.ReadResponse(statusTransferComplete); err != nil {
		return "", err
	}

	return message.data, nil
}

// Delete deletes a file from the FTP server.
func (c *FtpClient) Delete(filename string) (string, error) {
	command := fmt.Sprintf("DELE %s", filename)
	_, message, err := c.expectResponse(command, statusDeleteSuccess)
	return message, err
}

// MakeDirectory creates a folder on the FTP server.
func (c *FtpClient) MakeDirectory(path string) (string, error) {
	command := fmt.Sprintf("MKD %s", path)
	_, message, err := c.expectResponse(command, statusDirectorySuccess)
	return message, err
}

// RemoveDirectory deletes a folder from the FTP server.  Server
// implementations may require the folder to be empty before deletion.
func (c *FtpClient) RemoveDirectory(path string) (string, error) {
	command := fmt.Sprintf("RMD %s", path)
	_, message, err := c.expectResponse(command, statusDeleteSuccess)
	return message, err
}

// GetCurrentDirectory obtains the current working directory on the server.
func (c *FtpClient) GetCurrentDirectory() (string, error) {
	command := fmt.Sprintf("PWD")
	_, message, err := c.expectResponse(command, statusDirectorySuccess)
	return message, err
}

// ChangeDirectory switches the current working directory of the client on the
// server.
func (c *FtpClient) ChangeDirectory(path string) (string, error) {
	command := fmt.Sprintf("CWD %s", path)
	_, message, err := c.expectResponse(command, statusDirectoryChange)
	return message, err
}

// Quit tells the FTP server that it is about to disconnect.  On success, this
// method calls Disconnect.
func (c *FtpClient) Quit() error {
	if _, _, err := c.expectResponse("QUIT", statusInformative); err != nil {
		return err
	}
	if err := c.Disconnect(); err != nil {
		return err
	}
	return nil
}

// Disconnect disconnects from the FTP server.
func (c *FtpClient) Disconnect() error {
	if err := c.connection.Close(); err != nil {
		return err
	}
	return nil
}

// passiveResponseToHost a passive mode IP/port combo given from the FTP server
// and formats it into a host/port combo that net.Dial can connect with.
func passiveResponseToHost(response string) (string, error) {
	// Pulls out host/port combo, as in "Entering Passive Mode (192,168,1,6,82,110)."
	re := regexp.MustCompile("(\\d{1,3}),(\\d{1,3}),(\\d{1,3}),(\\d{1,3}),(\\d{1,3}),(\\d{1,3})")
	matches := re.FindStringSubmatch(response)

	// First four numbers are the IP address
	ip := strings.Join(matches[1:5], ".")
	// Fifth number is the upper byte of the 16-bit port number
	upperPortByte, err := strconv.Atoi(matches[5])
	if err != nil {
		return "", err
	}
	// Sixth byte is the lower byte of the 16-bit port number
	lowerPortByte, err := strconv.Atoi(matches[6])
	if err != nil {
		return "", err
	}
	port := upperPortByte<<8 + lowerPortByte

	return net.JoinHostPort(ip, strconv.Itoa(port)), nil
}

// passiveMode requests the FTP server open a passive data connection.
func (c *FtpClient) passiveMode() (string, error) {
	_, message, err := c.expectResponse("PASV", statusEnterPassiveMode)
	if err != nil {
		return "", err
	}

	// The returned message contains a host/port combo in the form of
	// e.g. (192,168,1,12,34,29).  Transform it to a normal IP/port format
	host, err := passiveResponseToHost(message)
	return host, err
}

// passiveConnection establishes a secondary connection with the FTP server to
// send data across.  This is meant to be run as a goroutine.
func passiveConnection(host string, data chan passiveData) (net.Conn, error) {
	// Establish the data connection
	conn, err := net.Dial("tcp", host)
	if err != nil {
		// Something went wrong; abort
		data <- passiveData{"", err}
		return nil, err
	}

	// Tell the main routine connection is established
	data <- passiveData{"", nil}

	// Wait for all clear from main routine
	if message := <-data; message.err != nil {
		// Something went wrong in main routine; abort
		fmt.Println("Error from main routine:", message.err)
		close(data)
		conn.Close()
		return nil, message.err
	}

	return conn, nil
}

// passiveRead reads all data sent by the FTP server via passive data
// connection
func passiveRead(host string, data chan passiveData) {
	// Establish connection
	conn, err := passiveConnection(host, data)
	if err != nil {
		return
	}

	// Read bytes rom server
	bytes, err := ioutil.ReadAll(conn)
	if err != nil {
		data <- passiveData{"", err}
	}
	conn.Close()

	// Return bytes to main routine
	// TODO this should really be sent as bytes,
	// in case there is an error in []byte -> string handling
	data <- passiveData{string(bytes), nil}
	close(data)
}

// passiveWrite writes contents in its entirety to the FTP server via passive
// data connection
func passiveWrite(host string, data chan passiveData, contents []byte) {
	// Establish connection
	conn, err := passiveConnection(host, data)
	if err != nil {
		return
	}

	// Write bytes to server
	n, err := conn.Write(contents)
	if err != nil {
		data <- passiveData{"", err}
	}
	conn.Close()

	// Return to main routine success
	message := fmt.Sprintf("Bytes sent: %d", n)
	data <- passiveData{message, nil}
	close(data)
}
