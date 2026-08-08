package sourcecontrol

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// BranchPrefix is the namespace for every Loom-managed stack branch.
const BranchPrefix = "loom/stack"

// StackBranchPrefix returns the branch-name prefix shared by every task in a stack.
func StackBranchPrefix(stackID StackID) string {
	return BranchPrefix + "/" + sanitizeRefSegment(stackID) + "/"
}

// OutputBranchName returns the readable deterministic branch for a task. The
// collision-aware AssignBranch result is the value persisted on StackNode.
func OutputBranchName(stackID StackID, taskID string) string {
	return BranchPrefix + "/" + sanitizeRefSegment(stackID) + "/" + sanitizeRefSegment(taskID)
}

// AssignBranch returns the stable available branch for taskID within stackID.
func AssignBranch(stackID StackID, taskID string, taken map[string]struct{}) string {
	readable := OutputBranchName(stackID, taskID)
	if _, clash := taken[readable]; !clash {
		return readable
	}
	suffixed := readable + "-" + shortStackHash(taskID)
	for index := 0; ; index++ {
		if _, clash := taken[suffixed]; !clash {
			return suffixed
		}
		suffixed = readable + "-" + shortStackHash(taskID+"#"+stackIndex(index))
	}
}

func shortStackHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:6]
}

func stackIndex(index int) string {
	if index == 0 {
		return "0"
	}
	var buffer [20]byte
	position := len(buffer)
	for index > 0 {
		position--
		buffer[position] = byte('0' + index%10)
		index /= 10
	}
	return string(buffer[position:])
}

func sanitizeRefSegment(value string) string {
	var builder strings.Builder
	builder.Grow(len(value))
	for _, character := range value {
		switch {
		case character >= 'a' && character <= 'z',
			character >= 'A' && character <= 'Z',
			character >= '0' && character <= '9',
			character == '.', character == '_', character == '-':
			builder.WriteRune(character)
		default:
			builder.WriteByte('-')
		}
	}
	result := builder.String()
	result = strings.ReplaceAll(result, "..", "-")
	result = strings.ReplaceAll(result, "@{", "-")
	result = strings.Trim(result, "-.")
	result = strings.TrimSuffix(result, ".lock")
	result = strings.Trim(result, "-.")
	if result == "" {
		return "unit"
	}
	return result
}
