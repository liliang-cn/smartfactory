package adapters

import "smartfactory/internal/tags"

// Forward decides whether a command reaches a source's equipment. Operators and MCP clients
// reach everything; visitors (and the automatic undo of their actions) only reach public
// sources, i.e. emulated equipment.
func Forward(public bool, c tags.Command) bool {
	return public || (c.By != "visitor" && c.By != "auto")
}
