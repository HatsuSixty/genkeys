package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path"
	"strconv"
	"strings"
	"unicode"
)

const USAGE = `USAGE: genkeys <COMP/WM> [CONFIG]
genkeys is a program that reads a file containing keybinding definitions and outputs a config file compatible with many wayland compositors/window managers.

	COMP/WM   Make genkeys dump the keybinding definitions in the configuration format used by <COMP/WM>.
		Supported compositors/window managers are:
			sway/i3
			hyprland
			all
		If 'help' is provided instead, it will print this help.
	CONFIG    The file containing the keybinding definitons. Defaults to '$HOME/.config/genkeys.gnks'. For more details, see 'help key_defs'.

	This program is also capable of saving the generated configs in a specified file. For more details, see 'help configuring'.`

const CONFIGURING_USAGE = `Configuring:
genkeys will try to find its configuration file at '$HOME/.config/genkeys.json'. As you can see, genkeys is configured using the json format. Here's how it's configured:
	{
		"WriteToFile": true,
		"HyprlandPath": "/home/user/.config/hypr/keys.conf",
		"SwayPath": "/home/user/.config/sway/keys.conf"
	}
WriteToFile: Is a boolean indicating whether genkeys should write its output to a file or not.
HyprlandPath/SwayPath: These are the paths of the files where genkeys should write its output to.`

const KEYBINDINGS_USAGE = `Defining keybindings:
Keybindings are defined the following way:
	bind "<keys>" "<shell command>"
Where <keys> are the keys that should be pressed in order to run <shell command>. Separated by spaces.
Example:
	bind "Super Shift Print" "slurp | grim -g - $(xdg-user-dir PICTURES)/screenshot.png"`

func isDigitsOnly(s string) bool {
	if len(s) == 0 { return false }
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func die(msg string, a ...any) {
	fmt.Fprintf(os.Stderr, fmt.Sprintf("%s\n", msg), a...)
	os.Exit(1)
}

func readFileToString(fpath string) (filecontent string) {
	b, err := ioutil.ReadFile(fpath)
	if err != nil {
		die("ERROR: Could not open file `%s`: %s", fpath, err)
	}
	filecontent = string(b)
	return
}

func fileExists(file string) bool {
	_, err := os.Stat(file)
	return err == nil
}

func getStream(file string) *os.File {
	stream, err := os.Create(file)
	if err != nil {
		die("ERROR: Could not open file `%s`: %s", file, err)
	}
	return stream
}

type TokenKind int
const (
	TOKEN_WORD TokenKind = iota
	TOKEN_STR  TokenKind = iota
)

type TokenLocation struct {
	File string
	Row  int
	Col  int
}

type Token struct {
	Kind TokenKind
	Text string
	Loc  TokenLocation
}

func lexerAdvanceLoc(char rune, row int, col int) (int, int) {
	col += 1
	if char == '\n' {
		row += 1
		col = 1
	}
	return row, col
}

func lex(fname string, str string) []Token {
	var tokens []Token

	var tokenText string
	strCursor := 0

	location := TokenLocation{File: fname, Row: 1, Col: 1}

	for strCursor = 0; strCursor < len(str); strCursor++ {
		char := rune(str[strCursor])

		switch {
		case unicode.IsSpace(char):
			if tokenText != "" {
				token_loc := location
				token_loc.Col -= len(strings.TrimSpace(tokenText))
				tokens = append(tokens,
					Token{Kind: TOKEN_WORD, Text: strings.TrimSpace(tokenText), Loc: token_loc})
				tokenText = ""
			}
		case char == '"':
			strCursor += 1
			char = rune(str[strCursor])

			strLoc := location

			location.Row, location.Col =
				lexerAdvanceLoc(rune(str[strCursor]), location.Row, location.Col)

			for char != '"' && strCursor < (len(str) - 1) {
				location.Row, location.Col =
					lexerAdvanceLoc(rune(str[strCursor]), location.Row, location.Col)

				tokenText += string(char)
				strCursor += 1

				char = rune(str[strCursor])
			}

			if strCursor >= len(str) || str[strCursor] != '"' {
				die("%s:%d:%d: ERROR: Unclosed string",
					strLoc.File, strLoc.Row, strLoc.Col)
			}

			tokens = append(tokens,
				Token{Kind: TOKEN_STR, Text: tokenText, Loc: strLoc})
			tokenText = ""
		default:
			tokenText += string(char)
		}

		location.Row, location.Col =
			lexerAdvanceLoc(rune(str[strCursor]), location.Row, location.Col)
	}

	return tokens
}

type KeyKind int
const (
	KEY_PRINT KeyKind = iota
	KEY_SUPER KeyKind = iota
	KEY_SHIFT KeyKind = iota
	KEY_NUM   KeyKind = iota
	KEY_CHAR  KeyKind = iota
	KEY_ENTER KeyKind = iota
)

type Key struct {
	Kind KeyKind
	Char rune
	Num  int
}

func stringToKey(str string, loc TokenLocation) Key {
	switch (str) {
	case "Print": return Key{Kind: KEY_PRINT}
	case "Super": return Key{Kind: KEY_SUPER}
	case "Shift": return Key{Kind: KEY_SHIFT}
	case "Enter": return Key{Kind: KEY_ENTER}
	default:
		if strings.HasPrefix(str, "N_") {
			num := strings.TrimPrefix(str, "N_")
			if !isDigitsOnly(num) {
				die("%s:%d:%d: Invalid `N_` key",
					loc.File, loc.Row, loc.Col)
			}

			n, _ := strconv.Atoi(num)
			if n > 9 {
				die("%s:%d:%d: Keypads have only 9 keys",
					loc.File, loc.Row, loc.Col)
			}

			return Key{Kind: KEY_NUM, Num: n}
		} else {
			if len(str) != 1 {
				die("%s:%d:%d: Invalid character key",
					loc.File, loc.Row, loc.Col)
			}
			return Key{Kind: KEY_CHAR, Char: unicode.ToUpper(rune(str[0]))}
		}
	}
}

type Keybinding struct {
	Keys []Key
	Command string
	Loc TokenLocation
}

func parseConfig(tokens []Token) []Keybinding {
	keybindings := []Keybinding{}

	var i int
	for i = 0; i < len(tokens); i++ {
		t := tokens[i]

		switch (t.Kind) {
		case TOKEN_WORD:
			switch (t.Text) {
			case "bind":
				keybinding := Keybinding{}

				if (i + 1) >= len(tokens) {
					die("%s:%d:%d: Key combination not provided for command `bind`",
						t.Loc.File, t.Loc.Row, t.Loc.Col)
				}
				if (i + 2) >= len(tokens) {
					die("%s:%d:%d: Exec command not provided for command `bind`",
						t.Loc.File, t.Loc.Row, t.Loc.Col)
				}

				keycomb := tokens[i+1]
				if keycomb.Kind != TOKEN_STR {
					die("%s:%d:%d: Key combination must be a string",
						keycomb.Loc.File, keycomb.Loc.Row, keycomb.Loc.Col)
				}

				execcmd := tokens[i+2]
				if execcmd.Kind != TOKEN_STR {
					die("%s:%d:%d: Exec command must be a string",
						execcmd.Loc.File, execcmd.Loc.Row, execcmd.Loc.Col)
				}

				if strings.TrimSpace(keycomb.Text) == "" {
					die("%s:%d:%d: Key combination must have at least one key",
						keycomb.Loc.File, keycomb.Loc.Row, keycomb.Loc.Col)
				}

				keys := strings.Fields(keycomb.Text)
				for _, k := range keys {
					keybinding.Keys = append(keybinding.Keys, stringToKey(k, keycomb.Loc))
				}

				if k := keybinding.Keys[0].Kind;
				k == KEY_CHAR || k == KEY_ENTER {
					die("%s:%d:%d: Key combination cannot start with `character key` or `Enter`",
						keycomb.Loc.File, keycomb.Loc.Row, keycomb.Loc.Col)
				}

				keybinding.Command = strings.TrimSpace(execcmd.Text)
				keybinding.Loc = t.Loc

				keybindings = append(keybindings, keybinding)

				i += 2
			default:
				die("%s:%d:%d: Unknown command: `%s`",
					t.Loc.File, t.Loc.Row, t.Loc.Col, t.Text)
			}
		case TOKEN_STR:
			die("%s:%d:%d: Strings cannot be used as commands",
				t.Loc.File, t.Loc.Row, t.Loc.Col)
		}
	}

	return keybindings
}

func dumpNumKey(num int) string {
	switch (num) {
	case 8: return "KP_Up"
	case 2: return "KP_Down"
	case 4: return "KP_Left"
	case 6: return "KP_Right"
	case 5: return "KP_Begin"
	case 7: return "KP_Home"
	case 9: return "KP_Prior"
	case 1: return "KP_End"
	case 3: return "KP_Next"
	default: return "?"
	}
}

func dumpKeySway(key Key) string {
	switch (key.Kind) {
	case KEY_PRINT: return "Print"
	case KEY_SUPER: return "$mod"
	case KEY_SHIFT: return "Shift"
	case KEY_NUM: return dumpNumKey(key.Num)
	case KEY_CHAR: return string(unicode.ToUpper(key.Char))
	case KEY_ENTER: return "Return"
	default: return "?"
	}
}

func dumpKeyHyprland(key Key) string {
	switch (key.Kind) {
	case KEY_PRINT: return "Print"
	case KEY_SUPER: return "$mainMod"
	case KEY_SHIFT: return "SHIFT"
	case KEY_NUM: return dumpNumKey(key.Num)
	case KEY_CHAR: return string(unicode.ToUpper(key.Char))
	case KEY_ENTER: return "Return"
	default: return "?"
	}
}

func dumpKeydefsHyprland(keybindings []Keybinding, file io.Writer) {
	w := bufio.NewWriter(file)

	for _, ks := range keybindings {
		if len(ks.Keys) > 3 {
			die("%s:%d:%d: Hyprland keybindings cannot contain more than 3 keys",
				ks.Loc.File, ks.Loc.Row, ks.Loc.Col)
		}
	}

	for _, ks := range keybindings {
		fmt.Fprintf(w, "bind = ")
		// I know this is a little hacky
		// But what's more hacky than that is hyprland's config
		switch (len(ks.Keys)) {
		case 1:
			fmt.Fprintf(w, ", %s", dumpKeyHyprland(ks.Keys[0]))
		case 2:
			fmt.Fprintf(w, "%s, %s",
				dumpKeyHyprland(ks.Keys[0]), dumpKeyHyprland(ks.Keys[1]))
		case 3:
			fmt.Fprintf(w, "%s %s, %s",
				dumpKeyHyprland(ks.Keys[0]), dumpKeyHyprland(ks.Keys[1]), dumpKeyHyprland(ks.Keys[2]))
		}
		fmt.Fprintf(w, ", exec, sh -c %s\n", strconv.Quote(ks.Command))
	}

	if err := w.Flush(); err != nil {
		die("ERROR: Could not flush buffer: %s", err)
	}
}

func dumpKeydefsSway(keybindings []Keybinding, file io.Writer) {
	w := bufio.NewWriter(file)

	for _, ks := range keybindings {
		fmt.Fprintf(w, "bindsym ")
		for i, k := range ks.Keys {
			fmt.Fprintf(w, dumpKeySway(k))
			if i != (len(ks.Keys) - 1) {
				fmt.Fprintf(w, "+")
			}
		}
		fmt.Fprintf(w, " exec sh -c %s\n", strconv.Quote(ks.Command))
	}

	if err := w.Flush(); err != nil {
		die("ERROR: Could not flush buffer: %s", err)
	}
}

type KeyDefs []Keybinding

func compileFileIntoKeydefs(file string) KeyDefs {
	filecontent := readFileToString(file)
	return parseConfig(lex(file, filecontent))
}

type Configuration struct {
	WriteToFile bool
	HyprlandPath string
	SwayPath string
}

type ConfigFormat int
const (
	CONFIG_SWAY ConfigFormat = iota
	CONFIG_HYPR ConfigFormat = iota
	CONFIG_ALL  ConfigFormat = iota
)

func writeConfigHyprland(config Configuration, keydefs string) {
	if config.WriteToFile {
		if strings.TrimSpace(config.HyprlandPath) == "" {
			die("ERROR: `HyprlandPath` not defined in config")
		}

		dumpKeydefsHyprland(compileFileIntoKeydefs(keydefs), getStream(config.HyprlandPath))
	} else {
		dumpKeydefsHyprland(compileFileIntoKeydefs(keydefs), os.Stdout)
	}
}

func writeConfigSway(config Configuration, keydefs string) {
	if config.WriteToFile {
		if strings.TrimSpace(config.SwayPath) == "" {
			die("ERROR: `SwayPath` not defined in config")
		}

		dumpKeydefsSway(compileFileIntoKeydefs(keydefs), getStream(config.SwayPath))
	} else {
		dumpKeydefsSway(compileFileIntoKeydefs(keydefs), os.Stdout)
	}
}

func main() {
	cfgFormatStr := "all"
	if len(os.Args) > 1 {
		cfgFormatStr = os.Args[1]
	}

	var cfgFormat ConfigFormat

	switch (cfgFormatStr) {
	case "sway", "i3": cfgFormat = CONFIG_SWAY
	case "hyprland": cfgFormat = CONFIG_HYPR
	case "all": cfgFormat = CONFIG_ALL
	case "help":
		if len(os.Args) > 2 {
			switch (os.Args[2]) {
			case "configuring":
				fmt.Println(CONFIGURING_USAGE)
				os.Exit(0)
			case "key_defs":
				fmt.Println(KEYBINDINGS_USAGE)
				os.Exit(0)
			default:
				die("ERROR: Unknown help page: `%s`", os.Args[2])
			}
		}
		fmt.Println(USAGE)
		os.Exit(0)
	default:
		die("%s\nERROR: Unknown configuration format: `%s`", USAGE, cfgFormatStr)
	}

	fpath := path.Join(os.Getenv("HOME"), "/.config/genkeys.gnks")
	if len(os.Args) > 2 {
		fpath = os.Args[2]
	}

	config := Configuration{}

	configPath := path.Join(os.Getenv("HOME"), "/.config/genkeys.json")
	if fileExists(configPath) {
		reader := strings.NewReader(readFileToString(configPath))
		jsonErr := json.NewDecoder(reader).Decode(&config)
		if jsonErr != nil {
			die("ERROR: Could not parse config `%s`: %s", configPath, jsonErr)
		}
	}

	switch (cfgFormat) {
	case CONFIG_SWAY:
		writeConfigSway(config, fpath)
	case CONFIG_HYPR:
		writeConfigHyprland(config, fpath)
	case CONFIG_ALL:
		writeConfigSway(config, fpath)
		writeConfigHyprland(config, fpath)
	default:
		die("ERROR: Saving config format `%s` is not implemented", cfgFormatStr)
	}
}
