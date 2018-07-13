package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"strings"

	"github.com/chzyer/readline"
)

func inputRequired(rl *readline.Instance, prompt string) string {
	originalPrompt := rl.Config.Prompt
	rl.SetPrompt(prompt)
	defer rl.SetPrompt(originalPrompt)

	input := ""
	var err error
	for input == "" {
		input, err = rl.Readline()
		if err != nil {
			return ""
		}
		input = strings.TrimSpace(input)
	}

	return input
}

func defaultInput(rl *readline.Instance, prompt, def string) string {
	rlPrompt := fmt.Sprintf("%s [%s]: ", prompt, def)
	originalPrompt := rl.Config.Prompt
	rl.SetPrompt(rlPrompt)
	defer rl.SetPrompt(originalPrompt)

	input, err := rl.Readline()
	if err != nil {
		return ""
	}

	input = strings.TrimSpace(input)
	if input != "" {
		return input
	}

	return def
}

func minimumArguments(command string, args []string, required int) bool {
	if len(args) < required {
		fmt.Println(command, "requires", required, "argument(s).  Arguments given:", len(args))
		return false
	}
	return true
}

func handleClient(message string, err error) (quit bool) {
	if err != nil {
		fmt.Println(err)
		return true
	}
	fmt.Println(message)
	return false
}

func externalCommand(command string) (string, error) {
	var out bytes.Buffer
	cmd := exec.Command(command)
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", err
	}
	strOut := out.String()
	if !strings.HasSuffix(strOut, "\n") {
		strOut += "\n"
	}
	return strOut, nil
}

func executeCommand(c *FtpClient, command string, args []string) (quit bool) {
	switch command {
	case "quit":
		return true

	case "dir":
		fallthrough
	case "ls":
		fallthrough
	case "list":
		if quit := handleClient(c.List()); quit {
			return quit
		}

	case "chdir":
		fallthrough
	case "cd":
		if !minimumArguments(command, args, 1) {
			return false
		}
		if quit := handleClient(c.ChangeDirectory(args[0])); quit {
			return quit
		}

	case "pwd":
		if quit := handleClient(c.GetCurrentDirectory()); quit {
			return quit
		}

	case "mkdir":
		if !minimumArguments(command, args, 1) {
			return false
		}
		if quit := handleClient(c.MakeDirectory(args[0])); quit {
			return quit
		}

	case "rmdir":
		if !minimumArguments(command, args, 1) {
			return false
		}
		if quit := handleClient(c.RemoveDirectory(args[0])); quit {
			return quit
		}

	case "touch":
		if !minimumArguments(command, args, 1) {
			return false
		}
		if quit := handleClient(c.Store(args[0], []byte(""))); quit {
			return quit
		}

	case "cat":
		if !minimumArguments(command, args, 1) {
			return false
		}
		if quit := handleClient(c.Retrieve(args[0])); quit {
			return quit
		}

	case "delete":
		fallthrough
	case "rm":
		if !minimumArguments(command, args, 1) {
			return false
		}
		if quit := handleClient(c.Delete(args[0])); quit {
			return quit
		}

	case "get":
		if !minimumArguments(command, args, 2) {
			return false
		}
		contents, err := c.Retrieve(args[0])
		if err != nil {
			fmt.Println(err)
			return false
		}
		err = ioutil.WriteFile(args[1], []byte(contents), 0644)
		if err != nil {
			fmt.Println(err)
		}

	case "send":
		if !minimumArguments(command, args, 2) {
			return false
		}
		contents, err := ioutil.ReadFile(args[0])
		if err != nil {
			fmt.Println(err)
			return false
		}
		message, err := c.Store(args[1], []byte(contents))
		if err != nil {
			fmt.Println(err)
		}
		fmt.Println(message)

	case "lpwd":
		message, err := externalCommand("pwd")
		if err != nil {
			fmt.Println(err)
			return false
		}
		fmt.Print(message)

	case "ldir":
		fallthrough
	case "llist":
		fallthrough
	case "lls":
		message, err := externalCommand("ls")
		if err != nil {
			fmt.Println(err)
			return false
		}
		fmt.Print(message)

	case "lchdir":
		fallthrough
	case "lcd":
		if !minimumArguments(command, args, 1) {
			return false
		}
		if err := os.Chdir(args[0]); err != nil {
			fmt.Println(err)
		}

	}
	return false
}

func main() {
	rl, err := readline.New("> ")
	if err != nil {
		panic(err)
	}
	defer rl.Close()

	rl.HistoryDisable()
	// Get connection details
	host := inputRequired(rl, "Host: ")
	if host == "" {
		return
	}

	port := defaultInput(rl, "Port", "21")
	if port == "" {
		return
	}

	connectionString := fmt.Sprintf("%s:%s", host, port)

	username := inputRequired(rl, "Username: ")
	if username == "" {
		return
	}

	passwordBytes, err := rl.ReadPassword("Password: ")
	if err != nil {
		return
	}
	password := string(passwordBytes[:]) // []byte to string
	rl.HistoryEnable()

	// Connect
	c, message, err := Connect(connectionString, username, password)
	if err != nil {
		fmt.Println("Error on connection: ", err)
		return
	}
	defer c.Quit()
	fmt.Println(message)

	// Main loop
	for {
		line, err := rl.Readline()
		if err != nil {
			break
		}
		args := strings.Split(line, " ")
		if len(args) > 0 {
			command := args[0]
			if quit := executeCommand(c, command, args[1:]); quit {
				break
			}
		}
	}
}
