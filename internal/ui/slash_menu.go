package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

const menuMinRows = 3

type slashMenu struct { visible bool; items []SlashCommand; selected int }
func newSlashMenu() *slashMenu { return &slashMenu{} }
func (m *slashMenu) open(items []SlashCommand) { m.visible=true; m.items=items; if m.selected>=len(items)||m.selected<0 { m.selected=0 } }
func (m *slashMenu) close() { m.visible=false; m.items=nil; m.selected=0 }
func (m *slashMenu) next() { if len(m.items)>0 { m.selected=(m.selected+1)%len(m.items) } }
func (m *slashMenu) prev() { if len(m.items)>0 { m.selected=(m.selected-1+len(m.items))%len(m.items) } }
func (m *slashMenu) currentCommand() (SlashCommand,bool) { if !m.visible||len(m.items)==0||m.selected<0||m.selected>=len(m.items) { return SlashCommand{},false }; return m.items[m.selected],true }

type slashMenuShape struct { rows,start,end int }
func (m *slashMenu) shape(maxRows ...int) slashMenuShape {
	if !m.visible||len(m.items)==0 { return slashMenuShape{} }
	full:=len(m.items)+2; rows:=full
	if len(maxRows)>0&&maxRows[0]>0 { rows=maxRows[0] }
	if rows>full { rows=full }; if rows<menuMinRows { rows=menuMinRows }
	itemRows:=rows-2; start:=0
	if itemRows>0&&len(m.items)>itemRows { start=m.selected-itemRows/2; if start<0 { start=0 }; if start+itemRows>len(m.items) { start=len(m.items)-itemRows; if start<0 { start=0 } } }
	return slashMenuShape{rows:rows,start:start,end:min(start+itemRows,len(m.items))}
}
func (m *slashMenu) lineCount(maxRows ...int) int { return m.shape(maxRows...).rows }

func (m *slashMenu) view(width int, maxRows ...int) string {
	if !m.visible||len(m.items)==0 { return "" }
	w:=width; if w<20 { w=20 }
	mode:=widthMode(w); menuW:=w
	if mode!=WidthWide&&menuW>50 { menuW=50 }
	header:="── Commands "; dashes:=menuW-ansi.StringWidth(header); if dashes<0 { dashes=0 }
	headerLine:=header+strings.Repeat("─",dashes); footerLine:=strings.Repeat("─",menuW); shape:=m.shape(maxRows...)
	var b strings.Builder; b.WriteString(dim.Render(headerLine)); b.WriteByte('\n')
	for i:=shape.start;i<shape.end;i++ {
		cmd:=m.items[i]; prefix:="  "; var line string
		switch mode {
		case WidthNarrow: line=cmd.Usage
		default: line=fmt.Sprintf("%-12s %s",cmd.Usage,cmd.Description)
		}
		maxLine:=menuW-2; if maxLine<4 { maxLine=4 }; if ansi.StringWidth(line)>maxLine { line=ansi.Truncate(line,maxLine,"…") }
		if i==m.selected { prefix="> "; b.WriteString(good.Render(prefix+line)) } else { b.WriteString(dim.Render(prefix+line)) }; b.WriteByte('\n')
	}
	b.WriteString(dim.Render(footerLine)); return b.String()
}
