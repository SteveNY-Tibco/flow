package instance

import (
	"strconv"

	"github.com/project-flogo/core/action"
	"github.com/project-flogo/core/data"
	"github.com/project-flogo/core/data/metadata"
	"github.com/project-flogo/core/data/resolve"
	"github.com/project-flogo/core/support/logger"
	"github.com/project-flogo/flow/definition"
	"github.com/project-flogo/flow/model"
)

type Instance struct {
	subFlowId int

	master *IndependentInstance //needed for change tracker
	host   interface{}

	isHandlingError bool

	status  model.FlowStatus
	flowDef *definition.Definition
	flowURI string //needed for serialization

	taskInsts map[string]*TaskInst
	linkInsts map[int]*LinkInst

	forceCompletion bool
	returnData      map[string]interface{}
	returnError     error

	attrs         map[string]interface{}
	activityAttrs map[string]map[string]data.TypedValue

	resultHandler action.ResultHandler
}

func (inst *Instance) GetDetails() data.StringsMap {
	panic("implement me")
}

func (inst *Instance) FlowURI() string {
	return inst.flowURI
}

func (inst *Instance) Name() string {
	return inst.flowDef.Name()
}

func (inst *Instance) ID() string {
	if inst.subFlowId > 0 {
		return inst.master.id + "-" + strconv.Itoa(inst.subFlowId)
	}

	return inst.master.id
}

// InitActionContext initialize the action context, should be initialized before execution
func (inst *Instance) SetResultHandler(handler action.ResultHandler) {
	inst.resultHandler = handler
}

// FindOrCreateTaskData finds an existing TaskInst or creates ones if not found for the
// specified task the task environment
func (inst *Instance) FindOrCreateTaskData(task *definition.Task) (taskInst *TaskInst, created bool) {

	taskInst, ok := inst.taskInsts[task.ID()]

	created = false

	if !ok {
		taskInst = NewTaskInst(inst, task)
		inst.taskInsts[task.ID()] = taskInst
		inst.master.ChangeTracker.trackTaskData(inst.subFlowId, &TaskInstChange{ChgType: CtAdd, ID: task.ID(), TaskInst: taskInst})

		created = true
	}

	return taskInst, created
}

// FindOrCreateLinkData finds an existing LinkInst or creates ones if not found for the
// specified link the task environment
func (inst *Instance) FindOrCreateLinkData(link *definition.Link) (linkInst *LinkInst, created bool) {

	linkInst, ok := inst.linkInsts[link.ID()]
	created = false

	if !ok {
		linkInst = NewLinkInst(inst, link)
		inst.linkInsts[link.ID()] = linkInst
		inst.master.ChangeTracker.trackLinkData(inst.subFlowId, &LinkInstChange{ChgType: CtAdd, ID: link.ID(), LinkInst: linkInst})
		created = true
	}

	return linkInst, created
}

func (inst *Instance) releaseTask(task *definition.Task) {
	delete(inst.taskInsts, task.ID())
	inst.master.ChangeTracker.trackTaskData(inst.subFlowId, &TaskInstChange{ChgType: CtDel, ID: task.ID()})
	links := task.FromLinks()

	for _, link := range links {
		delete(inst.linkInsts, link.ID())
		inst.master.ChangeTracker.trackLinkData(inst.subFlowId, &LinkInstChange{ChgType: CtDel, ID: link.ID()})
	}
}

/////////////////////////////////////////
// Instance - activity.Host Implementation

// IOMetadata get the input/output metadata of the activity host
func (inst *Instance) IOMetadata() *metadata.IOMetadata {
	return inst.flowDef.Metadata()
}

func (inst *Instance) Reply(replyData map[string]interface{}, err error) {
	if inst.resultHandler != nil {
		inst.resultHandler.HandleResult(replyData, err)
	}
}

func (inst *Instance) Return(returnData map[string]interface{}, err error) {
	inst.forceCompletion = true
	inst.returnData = returnData
	inst.returnError = err
}

func (inst *Instance) WorkingData() data.Scope {
	return inst
}

func (inst *Instance) GetResolver() resolve.CompositeResolver {
	return definition.GetDataResolver()
}

func (inst *Instance) GetError() error {
	return inst.returnError
}

func (inst *Instance) GetReturnData() (map[string]interface{}, error) {

	if inst.returnData == nil {

		//construct returnData from instance attributes
		md := inst.flowDef.Metadata()

		if md != nil && md.Output != nil {

			inst.returnData = make(map[string]interface{})
			for name, _ := range md.Output {
				piAttr, exists := inst.attrs[name]
				if exists {
					inst.returnData[name] = piAttr
				}
			}
		}
	}

	return inst.returnData, inst.returnError
}

/////////////////////////////////////////
// Instance - FlowContext Implementation

// Status returns the current status of the Flow Instance
func (inst *Instance) Status() model.FlowStatus {
	return inst.status
}

func (inst *Instance) SetStatus(status model.FlowStatus) {

	inst.status = status
	inst.master.ChangeTracker.SetStatus(inst.subFlowId, status)
}

// FlowDefinition returns the Flow definition associated with this context
func (inst *Instance) FlowDefinition() *definition.Definition {
	return inst.flowDef
}

// TaskInstances get the task instances
func (inst *Instance) TaskInstances() []model.TaskInstance {

	taskInsts := make([]model.TaskInstance, 0, len(inst.taskInsts))
	for _, value := range inst.taskInsts {
		taskInsts = append(taskInsts, value)
	}
	return taskInsts
}

/////////////////////////////////////////
// Instance - data.Scope Implementation

func (inst *Instance) GetValue(name string) (value interface{}, exists bool) {

	if inst.attrs != nil {
		attr, found := inst.attrs[name]

		if found {
			return attr, true
		}
	}

	return inst.flowDef.GetAttr(name)
}

func (inst *Instance) SetValue(name string, value interface{}) error {

	if inst.attrs == nil {
		inst.attrs = make(map[string]interface{})
	}

	if logger.DebugEnabled() {
		logger.Debugf("SetAttr - name: %s, value:%v\n", name, value)
	}

	inst.SetValue(name, value)

	if inst.master.trackingChanges {
		inst.master.ChangeTracker.AttrChange(inst.subFlowId, CtUpd, data.NewAttribute(name, data.TypeAny, value))
	}

	return nil

	//
	//existingAttr, exists := inst.GetAttr(attrName)
	//
	////todo: optimize, use existing attr
	//if exists {
	//	//todo handle error
	//	attr := data.NewTypedValue(existingAttr.Type(), value)
	//	inst.attrs[attrName] = attr
	//
	//	if inst.master.trackingChanges {
	//		inst.master.ChangeTracker.AttrChange(inst.subFlowId, CtUpd, data.NewAttribute(name, data.TypeAny, value))
	//	}
	//	return nil
	//}
	//
	//return fmt.Errorf("Attr [%s] does not exists", name)
}

//
//func (inst *Instance) GetValue(name string) (value interface{}, exists bool) {
//		if inst.attrs != nil {
//			attr, found := inst.attrs[attrName]
//
//			if found {
//				return attr, true
//			}
//		}
//
//		return inst.flowDef.GetAttr(attrName)
//}

//
//
//// GetAttr implements data.Scope.GetAttr
//func (inst *Instance) GetAttr(attrName string) (value typed.TypedValue, exists bool) {
//
//	if inst.attrs != nil {
//		attr, found := inst.attrs[attrName]
//
//		if found {
//			return attr, true
//		}
//	}
//
//	return inst.flowDef.GetAttr(attrName)
//}
//
//// SetAttrValue implements api.Scope.SetAttrValue
//func (inst *Instance) SetAttrValue(attrName string, value interface{}) error {
//	if inst.attrs == nil {
//		inst.attrs = make(map[string]typed.TypedValue)
//	}
//
//	logger.Debugf("SetAttr - name: %s, value:%v\n", attrName, value)
//
//	existingAttr, exists := inst.GetAttr(attrName)
//
//	//todo: optimize, use existing attr
//	if exists {
//		//todo handle error
//		attr, _ := data.NewAttribute(attrName, existingAttr.Type(), value)
//		inst.attrs[attrName] = attr
//		if inst.master.trackingChanges {
//			inst.master.ChangeTracker.AttrChange(inst.subFlowId, CtUpd, data.NewAttribute(attrName, existingAttr.Type(), value))
//		}
//		return nil
//	}
//
//	return fmt.Errorf("Attr [%s] does not exists", attrName)
//}

//// AddAttr add a new attribute to the instance
//func (inst *Instance) AddAttr(attrName string, attrType data.Type, value interface{}) typed.TypedValue {
//	if inst.attrs == nil {
//		inst.attrs = make(map[string]typed.TypedValue)
//	}
//
//	logger.Debugf("AddAttr - name: %s, type: %s, value:%v", attrName, attrType, value)
//
//	var attr typed.TypedValue
//
//	existingAttr, exists := inst.GetAttr(attrName)
//
//	if exists {
//		attr = existingAttr
//		attr.SetValue(value)
//	} else {
//		//todo handle error
//		attr, _ = data.NewAttribute(attrName, attrType, value)
//		inst.attrs[attrName] = attr
//		if inst.master.trackingChanges {
//			inst.master.ChangeTracker.AttrChange(inst.subFlowId, CtAdd, data.NewAttribute(attrName, existingAttr.Type(), value))
//		}
//	}
//
//	return attr
//}

////////////

// UpdateAttrs updates the attributes of the Flow Instance
func (inst *Instance) UpdateAttrs(attrs map[string]interface{}) {

	if attrs != nil {

		logger.Debugf("Updating flow attrs: %v", attrs)

		if inst.attrs == nil {
			inst.attrs = make(map[string]interface{}, len(attrs))
		}

		for name, attr := range attrs {
			inst.attrs[name] = attr
		}
	}
}

/////////////
// FlowDetails

//// ReplyHandler returns the reply handler for the instance
//func (inst *Instance) ReplyHandler() activity.ReplyHandler {
//	return &SimpleReplyHandler{inst.resultHandler}
//}
//
//// SimpleReplyHandler is a simple ReplyHandler that is pass-thru to the action ResultHandler
//type SimpleReplyHandler struct {
//	resultHandler action.ResultHandler
//}
//
//// Reply implements ReplyHandler.Reply
//func (rh *SimpleReplyHandler) Reply(code int, replyData interface{}, err error) {
//
//	dataAttr, _ := data.NewAttribute("data", data.TypeAny, replyData)
//	codeAttr, _ := data.NewAttribute("code", data.TypeInteger, code)
//	resultData := map[string]typed.TypedValue{
//		"data": dataAttr,
//		"code": codeAttr,
//	}
//
//	rh.resultHandler.HandleResult(resultData, err)
//}
