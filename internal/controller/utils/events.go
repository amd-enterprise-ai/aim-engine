/*
MIT License

Copyright (c) 2025 Advanced Micro Devices, Inc.

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
*/

package controllerutils

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
)

type EventLevel string

const (
	LevelNone    EventLevel = ""
	LevelNormal  EventLevel = EventLevel(corev1.EventTypeNormal)
	LevelWarning EventLevel = EventLevel(corev1.EventTypeWarning)
)

func EmitConditionTransitions(
	recorder record.EventRecorder,
	obj runtime.Object,
	transitions []ConditionTransition,
	manager *ConditionManager,
) {
	if recorder == nil || manager == nil {
		return
	}

	for _, transition := range transitions {
		// We only care about conditions that now exist
		if transition.New == nil {
			continue
		}
		newCondition := transition.New

		// Look up the event level chosen when the new condition was set
		level := manager.EventLevelFor(newCondition.Type)
		if level == LevelNone {
			// No event for this condition
			continue
		}

		eventType := string(level) // LevelNormal / LevelWarning are already corev1 event types

		reason := fmt.Sprintf("%s%s", newCondition.Type, newCondition.Reason)
		message := newCondition.Message
		if message == "" {
			message = fmt.Sprintf("Condition %s is %s (reason=%s)", newCondition.Type, newCondition.Status, newCondition.Reason)
		}

		recorder.Event(obj, eventType, reason, message)
	}
}
