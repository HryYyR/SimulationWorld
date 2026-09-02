package god

import (
	"ecosim/internal/core"
)

type Command = core.Command
type SpawnPayload = core.SpawnPayload
type RemovePayload = core.RemovePayload
type WeatherPayload = core.WeatherPayload
type ParamPayload = core.ParamPayload

type Queue struct {
	commands []Command
	nextID   int
}

func NewQueue() *Queue { return &Queue{nextID: 1} }

func (q *Queue) Push(cmd Command) Command {
	cmd.ID = q.nextID
	q.nextID++
	q.commands = append(q.commands, cmd)
	return cmd
}

func (q *Queue) Commands() []Command {
	return append([]Command(nil), q.commands...)
}

func (q *Queue) PopLast() (Command, bool) {
	if len(q.commands) == 0 {
		return Command{}, false
	}
	cmd := q.commands[len(q.commands)-1]
	q.commands = q.commands[:len(q.commands)-1]
	return cmd, true
}
