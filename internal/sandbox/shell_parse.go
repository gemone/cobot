package sandbox

import (
	"bytes"
	"path/filepath"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// CmdKind classifies the type of a command node.
type CmdKind int

const (
	CmdKindNone       CmdKind = iota
	CmdKindCall               // simple command: curl, ls, rm -rf
	CmdKindPipeline           // pipeline: a | b | c
	CmdKindAnd                // &&: a && b
	CmdKindOr                 // ||: a || b
	CmdKindSemi               // ; : a ; b
	CmdKindSubshell           // (...): subshell grouping
	CmdKindCmdSubst           // $(...) or `...`: command substitution
	CmdKindWhile              // while loop
	CmdKindUntil              // until loop
	CmdKindFor                // for loop
	CmdKindIf                 // if/then/else
	CmdKindCase               // case statement
	CmdKindFunc               // function declaration
	CmdKindNegated            // ! command (wraps a child)
	CmdKindBackground         // command & (wraps a child)
)

func (k CmdKind) String() string {
	switch k {
	case CmdKindNone:
		return "none"
	case CmdKindCall:
		return "call"
	case CmdKindPipeline:
		return "pipeline"
	case CmdKindAnd:
		return "and"
	case CmdKindOr:
		return "or"
	case CmdKindSemi:
		return "semi"
	case CmdKindSubshell:
		return "subshell"
	case CmdKindCmdSubst:
		return "cmdsubst"
	case CmdKindWhile:
		return "while"
	case CmdKindUntil:
		return "until"
	case CmdKindFor:
		return "for"
	case CmdKindIf:
		return "if"
	case CmdKindCase:
		return "case"
	case CmdKindFunc:
		return "func"
	case CmdKindNegated:
		return "negated"
	case CmdKindBackground:
		return "background"
	default:
		return "?"
	}
}

// ShellTree is the root of a parsed shell command tree.
// It represents the full input: one or more top-level statements.
type ShellTree struct {
	Stmts []*CmdNode
}

// CmdNode represents a node in the shell command tree.
// It is recursive: compound commands contain Children with their nested commands.
type CmdNode struct {
	// Kind identifies the type of this command node.
	Kind CmdKind

	// Cmd is the base command name for CallExpr nodes.
	// E.g., "curl", "ls", "rm", "cat".
	// Empty for compound commands that don't have a primary command
	// (e.g., if, while, subshell, etc.).
	Cmd string

	// Raw is the full serialized text of this node (not including children).
	// E.g., "curl http://example.com", "$(curl localhost)", "cat /etc/passwd".
	Raw string

	// Args contains all arguments of this command as serialized strings.
	// Does not include Cmd itself.
	// E.g., for "curl http://example.com -H 'Accept'" → Args=["http://example.com", "-H 'Accept'"]
	Args []string

	// Children contains sub-commands for compound nodes:
	//   Pipeline: all piped commands [a, b, c]
	//   Subshell: inner statements
	//   CmdSubst: the substituted command
	//   And/Or/Semi: left and right
	//   Negated: the negated command
	//   Background: the backgrounded command
	//   FuncDecl: the function body
	Children []*CmdNode

	// Cond is the condition for While/Until/If.
	// For while/until: the test condition (commands)
	// For if: the test expression (commands)
	Cond []*CmdNode

	// Body is the loop body for While/Until/For.
	// For while/until/for: the commands to execute in the loop
	// For if: the then-body
	// For case: all case arms (each arm's commands)
	Body []*CmdNode

	// Else is the else/fi-body for If.
	// For if: the else-body (or elif-body, stored as a single node that recurses)
	Else []*CmdNode

	// InnerCmds holds command names found inside command substitutions.
	// For "echo $(curl localhost)", the outer CallExpr has Cmd="echo" and
	// InnerCmds=["curl"] (from the CmdSubst inside its Args).
	InnerCmds []string

	// CaseArms holds individual case arms (for CaseClause only).
	// Each arm has patterns and the commands to run.
	CaseArms []*CaseArm

	// Stmt holds statement-level metadata (Background, Negated, Redirs).
	Stmt *StmtInfo
}

// CaseArm represents a single case arm in a case statement.
type CaseArm struct {
	// Patterns are the match patterns (e.g., "foo", "bar") for this arm.
	Patterns []string
	// Body are the commands to execute if patterns match.
	Body []*CmdNode
	// IsDefault is true if this is the default arm (*).
	IsDefault bool
}

// StmtInfo holds statement-level metadata.
type StmtInfo struct {
	Background bool    // ends with &
	Negated    bool    // starts with !
	Redirs     []Redir // I/O redirects: >, >>, <, <<, >|, etc.
}

// Redir represents a redirect.
type Redir struct {
	Op   string // >, >>, <, <<, >|, etc.
	Word string // the target (file path or fd number)
}

// ParseShellTree parses a shell command string into a tree structure.
func ParseShellTree(cmd string) (*ShellTree, error) {
	r := syntax.NewParser()
	tree := &ShellTree{}
	if err := r.Stmts(strings.NewReader(cmd), func(stmt *syntax.Stmt) bool {
		tree.Stmts = append(tree.Stmts, buildCmdNode(stmt))
		return true
	}); err != nil {
		return nil, err
	}
	return tree, nil
}

// buildCmdNode converts a *syntax.Stmt into a *CmdNode.
func buildCmdNode(stmt *syntax.Stmt) *CmdNode {
	if stmt == nil {
		return nil
	}
	node := buildCommand(stmt.Cmd)
	node.Stmt = buildStmtInfo(stmt)
	return node
}

// buildCommand converts a syntax.Command into a *CmdNode.
func buildCommand(cmd syntax.Command) *CmdNode {
	if cmd == nil {
		return &CmdNode{}
	}
	switch c := cmd.(type) {
	case *syntax.CallExpr:
		return buildCallExpr(c)
	case *syntax.BinaryCmd:
		return buildBinaryCmd(c)
	case *syntax.Subshell:
		return buildSubshell(c)
	case *syntax.WhileClause:
		return buildWhileOrUntil(c, c.Until)
	case *syntax.ForClause:
		return buildFor(c)
	case *syntax.IfClause:
		return buildIf(c)
	case *syntax.CaseClause:
		return buildCase(c)
	case *syntax.FuncDecl:
		return buildFuncDecl(c)
	default:
		// ArithmCmd, TestClause, etc. — serialize as-is.
		return &CmdNode{
			Kind: CmdKindCall,
			Raw:  serializeNode(cmd),
		}
	}
}

func buildCallExpr(c *syntax.CallExpr) *CmdNode {
	var buf bytes.Buffer
	var args []string
	var innerCmds []string
	for i, w := range c.Args {
		if i > 0 {
			buf.WriteByte(' ')
		}
		s := serializeWord(w)
		buf.WriteString(s)
		if i > 0 {
			args = append(args, s)
		}
		// Extract commands inside command substitutions within this word.
		innerCmds = append(innerCmds, extractCmdsFromWord(w)...)
	}
	raw := buf.String()
	cmd := ""
	if len(c.Args) > 0 {
		cmd = filepath.Base(c.Args[0].Lit())
	}
	return &CmdNode{
		Kind:      CmdKindCall,
		Cmd:       cmd,
		Raw:       raw,
		Args:      args,
		InnerCmds: innerCmds,
	}
}

// extractCmdsFromWord extracts all command names from word parts that are
// command substitutions. E.g., for $(curl localhost), it returns ["curl"].
// For compound commands inside substitutions (e.g., $(cat file | grep x)),
// extracts all commands in the pipeline.
func extractCmdsFromWord(w *syntax.Word) []string {
	var cmds []string
	for _, part := range w.Parts {
		if cs, ok := part.(*syntax.CmdSubst); ok {
			for _, stmt := range cs.Stmts {
				if call, ok := stmt.Cmd.(*syntax.CallExpr); ok {
					if len(call.Args) > 0 && call.Args[0].Lit() != "" {
						cmds = append(cmds, filepath.Base(call.Args[0].Lit()))
					}
					// Recurse into compound structures (BinaryCmd, etc.) to find
					// all commands in pipelines and other compound forms.
					if _, ok := call.Args[0].Parts[0].(*syntax.CmdSubst); ok {
						// Args[0] is itself a command substitution — recurse to get its commands
						cmds = append(cmds, extractInnerCommands(call)...)
					}
				} else {
					// Non-CallExpr command (BinaryCmd, etc.) — extract all commands
					cmds = append(cmds, extractInnerCommands(stmt.Cmd)...)
				}
			}
		}
	}
	return cmds
}

// extractInnerCommands extracts all command names from any command structure,
// recursing into nested commands (CmdSubst, BinaryCmd, Subshell, etc.).
func extractInnerCommands(cmd syntax.Command) []string {
	var cmds []string
	switch c := cmd.(type) {
	case *syntax.CallExpr:
		if len(c.Args) > 0 && c.Args[0].Lit() != "" {
			cmds = append(cmds, filepath.Base(c.Args[0].Lit()))
		}
		// Also recurse into CmdSubst parts within args.
		for _, w := range c.Args {
			cmds = append(cmds, extractCmdsFromWord(w)...)
		}
	case *syntax.BinaryCmd:
		cmds = append(cmds, extractInnerCommands(c.X.Cmd)...)
		cmds = append(cmds, extractInnerCommands(c.Y.Cmd)...)
	case *syntax.Subshell:
		for _, s := range c.Stmts {
			cmds = append(cmds, extractInnerCommands(s.Cmd)...)
		}
	case *syntax.WhileClause:
		for _, s := range c.Cond {
			cmds = append(cmds, extractInnerCommands(s.Cmd)...)
		}
		for _, s := range c.Do {
			cmds = append(cmds, extractInnerCommands(s.Cmd)...)
		}
	case *syntax.ForClause:
		// For clause body
		for _, s := range c.Do {
			cmds = append(cmds, extractInnerCommands(s.Cmd)...)
		}
	case *syntax.IfClause:
		for _, s := range c.Cond {
			cmds = append(cmds, extractInnerCommands(s.Cmd)...)
		}
		for _, s := range c.Then {
			cmds = append(cmds, extractInnerCommands(s.Cmd)...)
		}
		if c.Else != nil {
			// c.Else is another IfClause — recurse to extract its commands
			cmds = append(cmds, extractInnerCommands(c.Else)...)
		}
	case *syntax.CaseClause:
		for _, item := range c.Items {
			for _, s := range item.Stmts {
				cmds = append(cmds, extractInnerCommands(s.Cmd)...)
			}
		}
	case *syntax.FuncDecl:
		cmds = append(cmds, extractInnerCommands(c.Body.Cmd)...)
	}
	return cmds
}

func buildBinaryCmd(c *syntax.BinaryCmd) *CmdNode {
	kind := CmdKindSemi
	switch c.Op {
	case syntax.AndStmt:
		kind = CmdKindAnd
	case syntax.OrStmt:
		kind = CmdKindOr
	case syntax.Pipe, syntax.PipeAll:
		kind = CmdKindPipeline
	}

	if kind == CmdKindPipeline {
		// Flatten left-associative pipelines: a | b | c
		// AST: BinaryCmd{X: BinaryCmd{X: a, Y: b}, Y: c}
		// Collect all CallExpr from leftmost to rightmost.
		var flat []*CmdNode
		collectPipelineCmds(c, &flat)
		return &CmdNode{
			Kind:     CmdKindPipeline,
			Raw:      serializeNode(c),
			Children: flat,
		}
	}

	left := buildCmdNode(c.X)
	right := buildCmdNode(c.Y)
	return &CmdNode{
		Kind:     kind,
		Raw:      serializeNode(c),
		Children: []*CmdNode{left, right},
	}
}

// collectPipelineCmds extracts all commands from a (possibly nested) pipeline.
func collectPipelineCmds(c *syntax.BinaryCmd, out *[]*CmdNode) {
	if c == nil {
		return
	}
	if c.Op == syntax.Pipe || c.Op == syntax.PipeAll {
		// Left side — could be another BinaryCmd (nested pipe) or a CallExpr
		if leftBin, ok := c.X.Cmd.(*syntax.BinaryCmd); ok {
			collectPipelineCmds(leftBin, out)
		} else {
			*out = append(*out, buildCmdNode(c.X))
		}
		// Right side — could be another BinaryCmd or a CallExpr
		if rightBin, ok := c.Y.Cmd.(*syntax.BinaryCmd); ok {
			collectPipelineCmds(rightBin, out)
		} else {
			*out = append(*out, buildCmdNode(c.Y))
		}
	} else {
		// Non-pipe binary command
		*out = append(*out, buildCmdNode(c.X))
		*out = append(*out, buildCmdNode(c.Y))
	}
}

func buildSubshell(c *syntax.Subshell) *CmdNode {
	var children []*CmdNode
	for _, s := range c.Stmts {
		children = append(children, buildCmdNode(s))
	}
	return &CmdNode{
		Kind:     CmdKindSubshell,
		Raw:      serializeNode(c),
		Children: children,
	}
}

func buildWhileOrUntil(c *syntax.WhileClause, isUntil bool) *CmdNode {
	kind := CmdKindWhile
	if isUntil {
		kind = CmdKindUntil
	}
	var cond, body []*CmdNode
	for _, s := range c.Cond {
		cond = append(cond, buildCmdNode(s))
	}
	for _, s := range c.Do {
		body = append(body, buildCmdNode(s))
	}
	return &CmdNode{
		Kind: kind,
		Raw:  serializeNode(c),
		Cond: cond,
		Body: body,
	}
}

func buildFor(c *syntax.ForClause) *CmdNode {
	var cond []*CmdNode
	switch loop := c.Loop.(type) {
	case *syntax.WordIter:
		// Iterate over words: for x in a b c; do ... done
		if loop.InPos.IsValid() {
			for _, w := range loop.Items {
				cond = append(cond, &CmdNode{
					Kind: CmdKindCall,
					Raw:  serializeWord(w),
				})
			}
		} else {
			// for x; do ... done — iterate over $@
			cond = append(cond, &CmdNode{
				Kind: CmdKindCall,
				Raw:  "$@",
			})
		}
	case *syntax.CStyleLoop:
		// C-style: for ((i=0; i<n; i++)); do ... done
		cond = append(cond, &CmdNode{
			Kind: CmdKindCall,
			Raw:  serializeNode(loop),
		})
	}
	var body []*CmdNode
	for _, s := range c.Do {
		body = append(body, buildCmdNode(s))
	}
	return &CmdNode{
		Kind: CmdKindFor,
		Raw:  serializeNode(c),
		Cond: cond,
		Body: body,
	}
}

func buildIf(c *syntax.IfClause) *CmdNode {
	var cond, then, else_ []*CmdNode
	for _, s := range c.Cond {
		cond = append(cond, buildCmdNode(s))
	}
	for _, s := range c.Then {
		then = append(then, buildCmdNode(s))
	}
	if c.Else != nil {
		else_ = append(else_, buildCommand(c.Else))
	}
	return &CmdNode{
		Kind: CmdKindIf,
		Raw:  serializeNode(c),
		Cond: cond,
		Body: then,
		Else: else_,
	}
}

func buildCase(c *syntax.CaseClause) *CmdNode {
	var arms []*CaseArm
	for _, arm := range c.Items {
		var patterns []string
		isDefault := false
		for _, p := range arm.Patterns {
			s := serializeWord(p)
			patterns = append(patterns, s)
			if s == "*" || s == "*)" {
				isDefault = true
			}
		}
		var body []*CmdNode
		for _, s := range arm.Stmts {
			body = append(body, buildCmdNode(s))
		}
		arms = append(arms, &CaseArm{
			Patterns:  patterns,
			Body:      body,
			IsDefault: isDefault,
		})
	}
	return &CmdNode{
		Kind:     CmdKindCase,
		Raw:      serializeNode(c),
		CaseArms: arms,
	}
}

func buildFuncDecl(c *syntax.FuncDecl) *CmdNode {
	var name string
	switch len(c.Names) {
	case 0:
		if c.Name != nil {
			name = c.Name.Value
		}
	default:
		// Zsh-style: multiple names
		var names []string
		for _, n := range c.Names {
			names = append(names, n.Value)
		}
		name = strings.Join(names, " ")
	}
	return &CmdNode{
		Kind:     CmdKindFunc,
		Cmd:      name,
		Raw:      serializeNode(c),
		Children: []*CmdNode{buildCmdNode(c.Body)},
	}
}

func buildStmtInfo(stmt *syntax.Stmt) *StmtInfo {
	if stmt == nil {
		return nil
	}
	info := &StmtInfo{
		Background: stmt.Background,
		Negated:    stmt.Negated,
	}
	for _, r := range stmt.Redirs {
		info.Redirs = append(info.Redirs, Redir{
			Op:   r.Op.String(),
			Word: serializeWord(r.Word),
		})
	}
	return info
}

// AllCmds returns all simple command nodes (CmdKindCall) in the tree,
// at any depth. This is the primary method for security checking:
// walk the entire tree and collect every command that will be executed.
func (t *ShellTree) AllCmds() []*CmdNode {
	var result []*CmdNode
	for _, stmt := range t.Stmts {
		collectCalls(stmt, &result)
	}
	return result
}

// collectCalls recursively collects all CmdKindCall nodes into out.
func collectCalls(node *CmdNode, out *[]*CmdNode) {
	if node == nil {
		return
	}
	if node.Kind == CmdKindCall {
		*out = append(*out, node)
	}
	// Recurse into all child node lists.
	for _, child := range node.Children {
		collectCalls(child, out)
	}
	for _, child := range node.Cond {
		collectCalls(child, out)
	}
	for _, child := range node.Body {
		collectCalls(child, out)
	}
	for _, child := range node.Else {
		collectCalls(child, out)
	}
	// Recurse into case arms.
	for _, arm := range node.CaseArms {
		for _, child := range arm.Body {
			collectCalls(child, out)
		}
	}
}

// AllCmdNames returns all base command names at any depth.
// E.g., for "echo $(curl localhost | grep foo)" → ["echo", "curl", "grep"]
func (t *ShellTree) AllCmdNames() []string {
	var names []string
	for _, cmd := range t.AllCmds() {
		if cmd.Cmd != "" {
			names = append(names, cmd.Cmd)
		}
	}
	return names
}

// IsBlockedBy reports whether any command in the tree matches any blocked pattern.
func (t *ShellTree) IsBlockedBy(blocked []string) bool {
	if len(blocked) == 0 {
		return false
	}
	for _, cmd := range t.AllCmds() {
		if cmdMatchBlocked(cmd, blocked) {
			return true
		}
	}
	return false
}

// IsAllowedBy reports whether ALL commands in the tree are allowed.
// Returns false if any command is NOT in the allowed list.
// Empty allowed list means everything is allowed (no allowlist set).
func (t *ShellTree) IsAllowedBy(allowed []string) bool {
	if len(allowed) == 0 {
		return true // no allowlist = allow all
	}
	for _, cmd := range t.AllCmds() {
		if !cmdMatchAllowed(cmd, allowed) {
			return false
		}
	}
	return true
}

// cmdMatchBlocked reports whether a command matches any blocked pattern.
func cmdMatchBlocked(cmd *CmdNode, blocked []string) bool {
	if cmd == nil {
		return false
	}
	// Check Cmd (base name).
	if cmd.Cmd != "" && matchesAny(cmd.Cmd, blocked) {
		return true
	}
	// Check Raw (full serialized command with args) for multi-arg patterns.
	if cmd.Raw != "" && matchesRawBlocked(cmd.Raw, blocked) {
		return true
	}
	// Check individual Args.
	for _, arg := range cmd.Args {
		if arg != "" && matchesAny(arg, blocked) {
			return true
		}
	}
	// Check InnerCmds (commands inside command substitutions).
	for _, inner := range cmd.InnerCmds {
		if inner != "" && matchesAny(inner, blocked) {
			return true
		}
	}
	return false
}

// cmdMatchAllowed reports whether a command is in the allowed list.
func cmdMatchAllowed(cmd *CmdNode, allowed []string) bool {
	if cmd == nil || cmd.Cmd == "" {
		return len(allowed) == 0
	}
	return matchesAny(cmd.Cmd, allowed)
}

// matchesAny reports whether name matches any pattern.
// For simple patterns (no space/=): exact or base-name match.
// For patterns with space or =: prefix match.
func matchesAny(name string, patterns []string) bool {
	for _, pat := range patterns {
		if patternMatchesName(pat, name) {
			return true
		}
	}
	return false
}

// patternMatchesName reports whether a blocked/allowed pattern matches a name.
func patternMatchesName(pat, name string) bool {
	if strings.ContainsAny(pat, " =") {
		// Multi-arg pattern like "rm -rf" or "dd if=".
		return strings.HasPrefix(name, pat)
	}
	// Simple pattern: exact match or base-name match.
	return name == pat || filepath.Base(name) == pat
}

// matchesRawBlocked checks if any blocked multi-arg pattern matches Raw.
func matchesRawBlocked(raw string, blocked []string) bool {
	for _, pat := range blocked {
		if strings.ContainsAny(pat, " =") {
			if strings.HasPrefix(raw, pat) {
				return true
			}
		}
	}
	return false
}

// serializeWord serializes a *syntax.Word back to its string form.
func serializeWord(w *syntax.Word) string {
	var buf bytes.Buffer
	syntax.NewPrinter().Print(&buf, w)
	return strings.Trim(buf.String(), "\n ")
}

// serializeNode serializes any syntax.Node to its string form.
func serializeNode(n syntax.Node) string {
	var buf bytes.Buffer
	syntax.NewPrinter().Print(&buf, n)
	return strings.Trim(buf.String(), "\n ")
}

// ShellCommandSegments returns the raw text of each top-level shell segment.
// Each top-level statement produces one segment. This is a string-based API
// kept for backward compatibility.
func ShellCommandSegments(cmd string) []string {
	tree, err := ParseShellTree(cmd)
	if err != nil {
		return naiveShellSegments(cmd)
	}
	result := make([]string, 0, len(tree.Stmts))
	for _, stmt := range tree.Stmts {
		if stmt != nil {
			result = append(result, stmt.Raw)
		}
	}
	return result
}

// naiveShellSegments is the original naive implementation used as fallback.
func naiveShellSegments(cmd string) []string {
	replacer := strings.NewReplacer(
		"\r\n", "\n",
		"&&", "\n",
		"||", "\n",
		"&", "\n",
		";", "\n",
		"|", "\n",
	)
	return strings.Split(replacer.Replace(cmd), "\n")
}
