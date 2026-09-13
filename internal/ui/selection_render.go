package ui

// selectionPrefixWidth is the gutter every card reserves, selected or not.
// A constant gutter is what keeps the layout from twitching on every move:
// if only the selected card paid for it, each cursor step would reflow the
// card it left and the card it entered.
const selectionPrefixWidth = 2

// selectionPrefix returns the gutter for a card. ASCII only, and never the
// sole carrier of the selected state's meaning: the status row reports the
// position too, so NO_COLOR and screen readers lose nothing.
func selectionPrefix(selected bool) string {
	if selected {
		return "> "
	}
	return "  "
}
