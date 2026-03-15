package agent

import (
	"fmt"
	"sync"
	"time"
)

// BackgroundTask tracks a long-running command.
type BackgroundTask struct {
	ID        int
	Command   string
	StartedAt time.Time
	Done      bool
	Output    string
	Error     string
	mu        sync.Mutex
}

type TaskManager struct {
	tasks  map[int]*BackgroundTask
	nextID int
	mu     sync.Mutex
}

func NewTaskManager() *TaskManager {
	return &TaskManager{
		tasks:  make(map[int]*BackgroundTask),
		nextID: 1,
	}
}

func (tm *TaskManager) Start(command string, execute func(string) (string, error)) *BackgroundTask {
	tm.mu.Lock()
	task := &BackgroundTask{
		ID:        tm.nextID,
		Command:   command,
		StartedAt: time.Now(),
	}
	tm.tasks[task.ID] = task
	tm.nextID++
	tm.mu.Unlock()

	go func() {
		output, err := execute(command)
		task.mu.Lock()
		task.Done = true
		task.Output = output
		if err != nil {
			task.Error = err.Error()
		}
		task.mu.Unlock()
	}()

	return task
}

func (tm *TaskManager) List() []*BackgroundTask {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	var tasks []*BackgroundTask
	for _, t := range tm.tasks {
		tasks = append(tasks, t)
	}
	return tasks
}

func (tm *TaskManager) Get(id int) *BackgroundTask {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	return tm.tasks[id]
}

func (tm *TaskManager) Check() []*BackgroundTask {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	var completed []*BackgroundTask
	for id, t := range tm.tasks {
		t.mu.Lock()
		if t.Done {
			completed = append(completed, t)
			delete(tm.tasks, id)
		}
		t.mu.Unlock()
	}
	return completed
}

func (t *BackgroundTask) Status() string {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.Done {
		if t.Error != "" {
			return fmt.Sprintf("✗ Task #%d failed: %s", t.ID, t.Error)
		}
		return fmt.Sprintf("✓ Task #%d done (%s)", t.ID, time.Since(t.StartedAt).Round(time.Second))
	}
	return fmt.Sprintf("⏳ Task #%d running (%s): %s", t.ID, time.Since(t.StartedAt).Round(time.Second), t.Command)
}
