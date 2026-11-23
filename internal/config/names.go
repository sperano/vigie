package config

import "strings"

// nameSep separates the names of a NameList.
const nameSep = ","

// NameList is a flag.Value of names written <name>[,...]; setting it
// replaces the list, and blank names are dropped.
type NameList []string

// String lists the names in the form Set reads.
func (l *NameList) String() string {
	return strings.Join(*l, nameSep)
}

// Set replaces the list with the names in s; an empty s clears it.
func (l *NameList) Set(s string) error {
	var names []string
	for name := range strings.SplitSeq(s, nameSep) {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	*l = names
	return nil
}
