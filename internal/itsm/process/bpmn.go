package process

import (
	"encoding/xml"
	"fmt"
	"strings"
	"time"
)

// BPMN 2.0 兼容映射层: 将自研 JSON 流程定义(DAG steps + depends_on)与标准 BPMN 2.0 XML 互转。
// 目的: 将来可对接 Camunda/Flowable 或 bpmn-js 建模器, 支持导入/导出标准流程文件。
// 映射规则:
//   start → bpmn:startEvent          user → bpmn:userTask
//   service → bpmn:serviceTask       gateway → bpmn:exclusiveGateway
//   parallel → bpmn:parallelGateway  timer → bpmn:intermediateCatchEvent+timerEventDefinition
//   subflow → bpmn:callActivity      end → bpmn:endEvent
//   其他(notify 等) → bpmn:task
// 扩展字段(approvers/script_id/form_scope)存 bpmn:extensionElements/ops:*
// depends_on → bpmn:sequenceFlow(sourceRef→targetRef), 条件存 conditionExpression ${...}
// timer duration 在 JSON 为 "30m/1h/3d", 在 BPMN 为 ISO8601 "PT30M/PT1H/P3D"

const bpmnNS = "http://www.omg.org/spec/BPMN/20100524/MODEL"
const opsNS = "http://opsorigin.io/bpmn"

// --- JSON → XML ---

// ToBPMNXML 将自研流程定义序列化为 BPMN 2.0 XML。
func ToBPMNXML(p *Process) (string, error) {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	fmt.Fprintf(&b, `<bpmn:definitions xmlns:bpmn="%s" xmlns:ops="%s" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" targetNamespace="http://opsorigin.io/processes">`+"\n", bpmnNS, opsNS)
	fmt.Fprintf(&b, `  <bpmn:process id="%s" name="%s" isExecutable="true">`+"\n", xmlEscape(p.ID), xmlEscape(p.Name))
	for i := range p.Steps {
		b.WriteString(bpmnNodeXML(&p.Steps[i]))
	}
	flowID := 1
	for i := range p.Steps {
		st := &p.Steps[i]
		for _, dep := range st.DependsOn {
			cond := strings.TrimSpace(dep.Condition)
			if cond == "" || cond == "always" {
				fmt.Fprintf(&b, `    <bpmn:sequenceFlow id="f%d" sourceRef="%s" targetRef="%s"/>`+"\n", flowID, xmlEscape(dep.NodeID), xmlEscape(st.ID))
			} else {
				fmt.Fprintf(&b, `    <bpmn:sequenceFlow id="f%d" sourceRef="%s" targetRef="%s">`+"\n", flowID, xmlEscape(dep.NodeID), xmlEscape(st.ID))
				fmt.Fprintf(&b, `      <bpmn:conditionExpression xsi:type="bpmn:tFormalExpression">${%s}</bpmn:conditionExpression>`+"\n", xmlEscape(cond))
				b.WriteString("    </bpmn:sequenceFlow>\n")
			}
			flowID++
		}
	}
	b.WriteString("  </bpmn:process>\n")
	b.WriteString("</bpmn:definitions>\n")
	return b.String(), nil
}

func bpmnNodeXML(st *Step) string {
	id := xmlEscape(st.ID)
	name := xmlEscape(st.Name)
	inner := fmt.Sprintf(`id="%s" name="%s"`, id, name)
	var ext []string
	if len(st.Approvers) > 0 {
		ext = append(ext, fmt.Sprintf(`      <ops:approvers>%s</ops:approvers>`, xmlEscape(strings.Join(st.Approvers, ","))))
	}
	if st.ScriptID != "" {
		ext = append(ext, fmt.Sprintf(`      <ops:script_id>%s</ops:script_id>`, xmlEscape(st.ScriptID)))
	}
	if st.FormScope != "" {
		ext = append(ext, fmt.Sprintf(`      <ops:form_scope>%s</ops:form_scope>`, xmlEscape(st.FormScope)))
	}
	extXML := ""
	if len(ext) > 0 {
		extXML = "\n    <bpmn:extensionElements>\n" + strings.Join(ext, "\n") + "\n    </bpmn:extensionElements>"
	}
	var b strings.Builder
	switch st.Type {
	case StepStart:
		fmt.Fprintf(&b, "    <bpmn:startEvent %s/>\n", inner)
	case StepEnd:
		fmt.Fprintf(&b, "    <bpmn:endEvent %s/>\n", inner)
	case StepUser:
		b.WriteString("    <bpmn:userTask " + inner + ">" + extXML + "\n    </bpmn:userTask>\n")
	case StepService:
		b.WriteString("    <bpmn:serviceTask " + inner + ">" + extXML + "\n    </bpmn:serviceTask>\n")
	case StepGateway:
		fmt.Fprintf(&b, "    <bpmn:exclusiveGateway %s/>\n", inner)
	case StepParallel:
		fmt.Fprintf(&b, "    <bpmn:parallelGateway %s/>\n", inner)
	case StepTimer:
		dur := durationToISO8601(st.Duration)
		fmt.Fprintf(&b, "    <bpmn:intermediateCatchEvent %s>\n", inner)
		b.WriteString("      <bpmn:timerEventDefinition>\n")
		fmt.Fprintf(&b, `        <bpmn:timeDuration xsi:type="bpmn:tFormalExpression">%s</bpmn:timeDuration>`+"\n", xmlEscape(dur))
		b.WriteString("      </bpmn:timerEventDefinition>\n    </bpmn:intermediateCatchEvent>\n")
	case StepSubflow:
		fmt.Fprintf(&b, `    <bpmn:callActivity %s calledElement="%s"/>`+"\n", inner, xmlEscape(st.SubflowID))
	default:
		b.WriteString("    <bpmn:task " + inner + ">" + extXML + "\n    </bpmn:task>\n")
	}
	return b.String()
}

// --- XML → JSON ---

type bpmnDefinitions struct {
	XMLName xml.Name    `xml:"definitions"`
	Process bpmnProcess `xml:"process"`
}

type bpmnProcess struct {
	ID                       string           `xml:"id,attr"`
	Name                     string           `xml:"name,attr"`
	StartEvent               []bpmnElem       `xml:"startEvent"`
	EndEvent                 []bpmnElem       `xml:"endEvent"`
	UserTask                 []bpmnTask       `xml:"userTask"`
	ServiceTask              []bpmnTask       `xml:"serviceTask"`
	ExclusiveGateway         []bpmnElem       `xml:"exclusiveGateway"`
	ParallelGateway          []bpmnElem       `xml:"parallelGateway"`
	IntermediateCatchEvent   []bpmnTimer      `xml:"intermediateCatchEvent"`
	CallActivity             []bpmnCall       `xml:"callActivity"`
	Task                     []bpmnTask       `xml:"task"`
	SequenceFlow             []bpmnFlow       `xml:"sequenceFlow"`
}

type bpmnElem struct {
	ID   string `xml:"id,attr"`
	Name string `xml:"name,attr"`
}

type bpmnTask struct {
	ID        string   `xml:"id,attr"`
	Name      string   `xml:"name,attr"`
	Extension bpmnExt  `xml:"extensionElements"`
}

type bpmnExt struct {
	Approvers string `xml:"approvers"`
	ScriptID  string `xml:"script_id"`
	FormScope string `xml:"form_scope"`
}

type bpmnTimer struct {
	ID    string       `xml:"id,attr"`
	Name  string       `xml:"name,attr"`
	Timer bpmnTimerDef `xml:"timerEventDefinition"`
}

type bpmnTimerDef struct {
	Duration string `xml:"timeDuration"`
}

type bpmnCall struct {
	ID            string `xml:"id,attr"`
	Name          string `xml:"name,attr"`
	CalledElement string `xml:"calledElement,attr"`
}

type bpmnFlow struct {
	ID        string `xml:"id,attr"`
	Source    string `xml:"sourceRef,attr"`
	Target    string `xml:"targetRef,attr"`
	Condition string `xml:"conditionExpression"`
}

// FromBPMNXML 解析 BPMN 2.0 XML 为自研流程定义。
func FromBPMNXML(data string) (*Process, error) {
	def := &bpmnDefinitions{}
	if err := xml.Unmarshal([]byte(data), def); err != nil {
		return nil, fmt.Errorf("parse bpmn xml: %w", err)
	}
	p := &Process{ID: def.Process.ID, Name: def.Process.Name}
	if p.ID == "" {
		p.ID = "imported"
	}
	// 节点
	for _, e := range def.Process.StartEvent {
		p.Steps = append(p.Steps, Step{ID: e.ID, Name: e.Name, Type: StepStart})
	}
	for _, e := range def.Process.EndEvent {
		p.Steps = append(p.Steps, Step{ID: e.ID, Name: e.Name, Type: StepEnd})
	}
	for _, t := range def.Process.UserTask {
		p.Steps = append(p.Steps, Step{ID: t.ID, Name: t.Name, Type: StepUser,
			Approvers: splitApprovers(t.Extension.Approvers), FormScope: t.Extension.FormScope})
	}
	for _, t := range def.Process.ServiceTask {
		p.Steps = append(p.Steps, Step{ID: t.ID, Name: t.Name, Type: StepService, ScriptID: t.Extension.ScriptID})
	}
	for _, e := range def.Process.ExclusiveGateway {
		p.Steps = append(p.Steps, Step{ID: e.ID, Name: e.Name, Type: StepGateway})
	}
	for _, e := range def.Process.ParallelGateway {
		p.Steps = append(p.Steps, Step{ID: e.ID, Name: e.Name, Type: StepParallel})
	}
	for _, tm := range def.Process.IntermediateCatchEvent {
		p.Steps = append(p.Steps, Step{ID: tm.ID, Name: tm.Name, Type: StepTimer, Duration: durationFromISO8601(tm.Timer.Duration)})
	}
	for _, c := range def.Process.CallActivity {
		p.Steps = append(p.Steps, Step{ID: c.ID, Name: c.Name, Type: StepSubflow, SubflowID: c.CalledElement})
	}
	for _, t := range def.Process.Task {
		p.Steps = append(p.Steps, Step{ID: t.ID, Name: t.Name, Type: StepNotify, ScriptID: t.Extension.ScriptID})
	}
	// 边 → depends_on
	idToIdx := map[string]int{}
	for i, s := range p.Steps {
		idToIdx[s.ID] = i
	}
	for _, f := range def.Process.SequenceFlow {
		idx, ok := idToIdx[f.Target]
		if !ok {
			continue // 目标不在本流程
		}
		cond := strings.TrimSpace(f.Condition)
		cond = strings.TrimPrefix(cond, "${")
		cond = strings.TrimSuffix(cond, "}")
		cond = strings.TrimSpace(cond)
		if _, srcOK := idToIdx[f.Source]; !srcOK && cond == "" {
			continue // 外部源且无条件, 忽略
		}
		p.Steps[idx].DependsOn = append(p.Steps[idx].DependsOn, Dependency{NodeID: f.Source, Condition: cond})
	}
	return p, nil
}

func splitApprovers(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if v := strings.TrimSpace(part); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// --- duration 与 ISO8601 互转 ---

func durationToISO8601(s string) string {
	d, ok := parseStepDuration(s)
	if !ok {
		return ""
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	secs := int(d.Seconds()) % 60
	var b strings.Builder
	b.WriteString("P")
	if days > 0 {
		fmt.Fprintf(&b, "%dD", days)
	}
	if hours > 0 || mins > 0 || secs > 0 {
		b.WriteString("T")
		if hours > 0 {
			fmt.Fprintf(&b, "%dH", hours)
		}
		if mins > 0 {
			fmt.Fprintf(&b, "%dM", mins)
		}
		if secs > 0 {
			fmt.Fprintf(&b, "%dS", secs)
		}
	}
	if b.String() == "P" {
		return ""
	}
	return b.String()
}

func durationFromISO8601(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "P") {
		return ""
	}
	var total time.Duration
	body := s[1:]
	datePart, timePart := body, ""
	if idx := strings.Index(body, "T"); idx >= 0 {
		datePart, timePart = body[:idx], body[idx+1:]
	}
	num := ""
	apply := func(unit time.Duration) bool {
		if num == "" {
			return false
		}
		v, err := parseFloat64Safe(num)
		if err != nil {
			return false
		}
		total += time.Duration(v * float64(unit))
		num = ""
		return true
	}
	for _, r := range datePart {
		if r >= '0' && r <= '9' {
			num += string(r)
			continue
		}
		switch r {
		case 'D':
			if !apply(24 * time.Hour) {
				return ""
			}
		default:
			return ""
		}
	}
	for _, r := range timePart {
		if r >= '0' && r <= '9' {
			num += string(r)
			continue
		}
		switch r {
		case 'H':
			if !apply(time.Hour) {
				return ""
			}
		case 'M':
			if !apply(time.Minute) {
				return ""
			}
		case 'S':
			if !apply(time.Second) {
				return ""
			}
		default:
			return ""
		}
	}
	if total <= 0 {
		return ""
	}
	return shortDuration(total)
}

// shortDuration 将时长转换为 "30m/1h30m/3d" 形式(与 JSON duration 字段一致)。
func shortDuration(d time.Duration) string {
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	secs := int(d.Seconds()) % 60
	var b strings.Builder
	if days > 0 {
		fmt.Fprintf(&b, "%dd", days)
	}
	if hours > 0 {
		fmt.Fprintf(&b, "%dh", hours)
	}
	if mins > 0 {
		fmt.Fprintf(&b, "%dm", mins)
	}
	if secs > 0 {
		fmt.Fprintf(&b, "%ds", secs)
	}
	if b.Len() == 0 {
		return "0s"
	}
	return b.String()
}

func parseFloat64Safe(s string) (float64, error) {
	var v float64
	var frac float64 = 0.1
	seenDot := false
	for _, r := range s {
		if r == '.' {
			if seenDot {
				return 0, fmt.Errorf("bad number")
			}
			seenDot = true
			continue
		}
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("bad number")
		}
		d := float64(r - '0')
		if seenDot {
			v += d * frac
			frac /= 10
		} else {
			v = v*10 + d
		}
	}
	return v, nil
}

func xmlEscape(s string) string {
	var b strings.Builder
	xml.EscapeText(&b, []byte(s))
	return b.String()
}
