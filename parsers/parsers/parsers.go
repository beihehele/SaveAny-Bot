package parsers

import (
	"fmt"
	"sync"

	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/pkg/parser"
)

type registryEntry struct {
	parser parser.Parser
	group  *Group
}

var (
	parsers       []registryEntry
	groups        = make(map[string]*Group)
	mu            sync.Mutex
	configOnce    sync.Once
	configParsers = func() {
		mu.Lock()
		defer mu.Unlock()
		if len(parsers) == 0 {
			return
		}
		for _, entry := range parsers {
			if configurable, ok := entry.parser.(parser.ConfigurableParser); ok {
				cfg := config.C().GetParserConfigByName(configurable.Name())
				if err := configurable.Configure(cfg); err != nil {
					fmt.Printf("Error configuring parser %s: %v\n", configurable.Name(), err)
				}
			}
		}
	}
)

func Add(p ...parser.Parser) {
	mu.Lock()
	defer mu.Unlock()
	for _, added := range p {
		parsers = append(parsers, registryEntry{parser: added})
	}
}

// Group owns the parsers loaded from one plugin source. A replaced group cannot
// append parsers later, even if an old invocation still holds registerParser.
type Group struct{ source string }

// NewGroup creates a group; Replace publishes it after its script/file is valid.
func NewGroup(source string) *Group { return &Group{source: source} }

// Replace swaps all parsers owned by this source while retaining its position
// relative to other sources and native parsers. It returns the retired parsers.
func (g *Group) Replace(p ...parser.Parser) []parser.Parser {
	mu.Lock()
	defer mu.Unlock()
	var retired []parser.Parser
	next := make([]registryEntry, 0, len(parsers)+len(p))
	added := make([]registryEntry, 0, len(p))
	for _, value := range p {
		added = append(added, registryEntry{parser: value, group: g})
	}
	inserted := false
	for _, existing := range parsers {
		if old := existing.group; old != nil && old.source == g.source {
			if !inserted {
				next = append(next, added...)
				inserted = true
			}
			retired = append(retired, existing.parser)
		} else {
			next = append(next, existing)
		}
	}
	if !inserted {
		next = append(next, added...)
	}
	groups[g.source] = g
	parsers = next
	return retired
}

// Add publishes a runtime registration only while this group is current.
func (g *Group) Add(p ...parser.Parser) bool {
	mu.Lock()
	defer mu.Unlock()
	if groups[g.source] != g {
		return false
	}
	for _, added := range p {
		parsers = append(parsers, registryEntry{parser: added, group: g})
	}
	return true
}

func Get() []parser.Parser {
	configOnce.Do(configParsers)
	mu.Lock()
	defer mu.Unlock()
	snapshot := make([]parser.Parser, 0, len(parsers))
	for _, entry := range parsers {
		snapshot = append(snapshot, entry.parser)
	}
	return snapshot
}
