// Package ops manages the structure on the host and the approval mechanism.
//
// The structure is the one from the README:
//
//	rules/        read-only in the VM
//	public/       read-only in the VM
//	approved/     read-only in the VM
//	rejected/     read-only in the VM
//	pending/      read-write
//	logs/         read-write
//
// SKELETON.
package ops

// Dirs are the directories created by "init".
// The value says whether the VM can write to them.
var Dirs = map[string]bool{
	"rules":    false,
	"public":   false,
	"approved": false,
	"rejected": false,
	"pending":  true,
	"logs":     true,
}

// Init creates the structure and the default configuration. Does not overwrite.
func Init(opsDir string) error { panic("not implemented") }

// Request is an approval request written by the agent into pending/.
type Request struct {
	ID     string
	Path   string
	Action string
}

// ListPending returns the requests in pending/.
func ListPending(opsDir string) ([]Request, error) { panic("not implemented") }

// Approve moves the request from pending/ to approved/.
//
// The move IS the act of approval. The agent has no write access to approved/,
// precisely so it cannot grant itself one.
func Approve(opsDir, id string) error { panic("not implemented") }

// Reject moves the request to rejected/. Final - a rejected request is not
// resent, not reworded, not split into smaller pieces.
func Reject(opsDir, id string) error { panic("not implemented") }

// RulesEmpty reports whether rules/ is empty.
//
// "up" refuses to start if it is: an agent with no written rules has no way
// to follow them.
func RulesEmpty(opsDir string) (bool, error) { panic("not implemented") }
