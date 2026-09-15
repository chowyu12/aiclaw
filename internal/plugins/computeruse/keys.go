package computeruse

// keyCodes maps named keys to macOS virtual key codes. A key outside this
// table and outside the printable range is rejected rather than guessed.
var keyCodes = map[string]int{
	"return": 36, "enter": 36, "tab": 48, "space": 49, "delete": 51,
	"backspace": 51, "escape": 53, "esc": 53, "forward_delete": 117,
	"left": 123, "right": 124, "down": 125, "up": 126,
	"home": 115, "end": 119, "page_up": 116, "page_down": 121,
	"f1": 122, "f2": 120, "f3": 99, "f4": 118, "f5": 96, "f6": 97,
	"f7": 98, "f8": 100, "f9": 101, "f10": 109, "f11": 103, "f12": 111,
}

// modifierNames maps accepted modifier spellings to AppleScript modifier
// names. Only these values ever reach a script.
var modifierNames = map[string]string{
	"cmd": "command", "command": "command", "meta": "command",
	"ctrl": "control", "control": "control",
	"alt": "option", "option": "option",
	"shift": "shift", "fn": "function",
}
