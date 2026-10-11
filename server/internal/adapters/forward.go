package adapters

import (
	"cmp"

	"smartfactory/internal/config"
	"smartfactory/internal/tags"
)

// Forward decides whether a command reaches a source's equipment. Operators and MCP clients
// reach everything; visitors (and the automatic undo of their actions) only reach public
// sources, i.e. emulated equipment.
func Forward(public bool, c tags.Command) bool {
	return public || (c.By != "visitor" && c.By != "auto")
}

// register tells the hub what a source is: emulated equipment or not, and the assets it takes
// commands for, so the command guard knows which commands reach a machine.
func register(hub *tags.Hub, src config.Source) {
	hub.SetPublic(src.Name, src.Public)
	var targets []string
	for _, b := range src.Commands {
		c, p := cmp.Or(b.Company, src.Company), cmp.Or(b.Plant, src.Plant)
		if c != "" && p != "" && b.Asset != "" {
			targets = append(targets, c+"/"+p+"/"+b.Asset)
		}
	}
	hub.AcceptCommands(src.Name, targets)
}
