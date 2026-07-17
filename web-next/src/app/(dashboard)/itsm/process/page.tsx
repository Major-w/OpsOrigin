'use client'

import { useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { FormDesigner, type FormField } from '@/components/lowcode/FormDesigner'
import { Plus, Trash2, GitBranch, ArrowRight, Pencil, Eye, ChevronLeft, Save, FileText, Settings } from 'lucide-react'
import { cn } from '@/lib/utils'

interface ProcessStep { id: string; name: string; approver: string; onSuccess: string; onReject: string }
interface Process {
  id: string
  name: string
  description: string
  steps: ProcessStep[]
  formFields: FormField[]
  createdAt: string
}

type ViewMode = 'list' | 'create' | 'edit' | 'detail'

export default function ITSMProcessPage() {
  const [processes, setProcesses] = useState<Process[]>([
    {
      id: '1',
      name: '标准变更流程',
      description: '适用于常规变更操作的审批流程',
      steps: [
        { id: '1', name: '提交申请', approver: '申请人', onSuccess: '运营审批', onReject: '退回' },
        { id: '2', name: '运营审批', approver: '运维负责人', onSuccess: '安全审批', onReject: '退回' },
        { id: '3', name: '安全审批', approver: '安全团队', onSuccess: '执行', onReject: '退回' },
        { id: '4', name: '执行', approver: '运维执行人', onSuccess: '完成', onReject: '回滚' },
      ],
      formFields: [
        { id: 'f1', type: 'input', title: '变更标题', name: 'title', required: true, placeholder: '请输入变更标题', colSpan: 12 },
        { id: 'f2', type: 'textarea', title: '变更描述', name: 'description', required: true, placeholder: '请详细描述变更内容', colSpan: 12 },
        { id: 'f3', type: 'select', title: '变更类型', name: 'type', required: true, placeholder: '请选择变更类型', colSpan: 6 },
        { id: 'f4', type: 'date', title: '计划执行时间', name: 'plan_time', required: false, placeholder: '请选择执行时间', colSpan: 6 },
      ],
      createdAt: '2026-01-15',
    },
    {
      id: '2',
      name: '紧急变更流程',
      description: '适用于紧急生产问题的快速审批流程',
      steps: [
        { id: '1', name: '提交申请', approver: '申请人', onSuccess: '主管审批', onReject: '退回' },
        { id: '2', name: '主管审批', approver: '部门主管', onSuccess: '执行', onReject: '退回' },
        { id: '3', name: '执行', approver: '运维执行人', onSuccess: '完成', onReject: '回滚' },
      ],
      formFields: [
        { id: 'f1', type: 'input', title: '问题标题', name: 'title', required: true, placeholder: '请输入问题标题', colSpan: 12 },
        { id: 'f2', type: 'textarea', title: '问题描述', name: 'description', required: true, placeholder: '请描述紧急问题', colSpan: 12 },
        { id: 'f3', type: 'select', title: '影响范围', name: 'impact', required: true, placeholder: '请选择影响范围', colSpan: 6 },
        { id: 'f4', type: 'radio', title: '是否需要回滚', name: 'need_rollback', required: true, placeholder: '请选择', colSpan: 6 },
      ],
      createdAt: '2026-02-01',
    },
  ])

  const [viewMode, setViewMode] = useState<ViewMode>('list')
  const [currentProcess, setCurrentProcess] = useState<Process | null>(null)
  const [form, setForm] = useState({ name: '', description: '' })
  const [formFields, setFormFields] = useState<FormField[]>([])
  const [steps, setSteps] = useState<ProcessStep[]>([])

  // 打开新建页面
  const openCreate = () => {
    setCurrentProcess(null)
    setForm({ name: '', description: '' })
    setFormFields([])
    setSteps([
      { id: '1', name: '提交申请', approver: '申请人', onSuccess: '审批', onReject: '退回' },
      { id: '2', name: '审批', approver: '审批人', onSuccess: '执行', onReject: '退回' },
      { id: '3', name: '执行', approver: '执行人', onSuccess: '完成', onReject: '回滚' },
    ])
    setViewMode('create')
  }

  // 打开编辑页面
  const openEdit = (process: Process) => {
    setCurrentProcess(process)
    setForm({ name: process.name, description: process.description })
    setFormFields([...process.formFields])
    setSteps([...process.steps])
    setViewMode('edit')
  }

  // 打开详情页面
  const openDetail = (process: Process) => {
    setCurrentProcess(process)
    setViewMode('detail')
  }

  // 保存
  const handleSave = () => {
    if (viewMode === 'create') {
      const newProcess: Process = {
        id: Date.now().toString(),
        name: form.name,
        description: form.description,
        steps,
        formFields,
        createdAt: new Date().toISOString().split('T')[0],
      }
      setProcesses([...processes, newProcess])
    } else if (viewMode === 'edit' && currentProcess) {
      setProcesses(processes.map(p =>
        p.id === currentProcess.id
          ? { ...p, name: form.name, description: form.description, steps, formFields }
          : p
      ))
    }
    setViewMode('list')
    setCurrentProcess(null)
  }

  // 删除
  const handleDelete = (id: string) => {
    setProcesses(processes.filter(p => p.id !== id))
    if (currentProcess?.id === id) {
      setCurrentProcess(null)
      setViewMode('list')
    }
  }

  // 步骤操作
  const addStep = () => {
    setSteps([...steps, {
      id: Date.now().toString(),
      name: `步骤${steps.length + 1}`,
      approver: '',
      onSuccess: '',
      onReject: '退回',
    }])
  }

  const updateStep = (id: string, updates: Partial<ProcessStep>) => {
    setSteps(steps.map(s => s.id === id ? { ...s, ...updates } : s))
  }

  const deleteStep = (id: string) => {
    setSteps(steps.filter(s => s.id !== id))
  }

  // 列表视图
  if (viewMode === 'list') {
    return (
      <div className="p-6 space-y-4">
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-2xl font-bold">流程定义</h1>
            <p className="text-sm text-muted-foreground">自定义审批流程与表单设计</p>
          </div>
          <Button onClick={openCreate}><Plus className="h-4 w-4 mr-1" />新建流程</Button>
        </div>

        {processes.length === 0 ? (
          <Card>
            <CardContent className="py-12 text-center text-muted-foreground">
              <GitBranch className="h-10 w-10 mx-auto mb-3 opacity-30" />
              <p>暂无流程定义</p>
              <p className="text-xs mt-1">点击"新建流程"创建审批流程</p>
            </CardContent>
          </Card>
        ) : (
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
            {processes.map(p => (
              <Card key={p.id} className="hover:border-emerald-500/20 transition-colors">
                <CardContent className="p-4">
                  <div className="flex items-start justify-between mb-3">
                    <div className="flex items-center gap-2">
                      <div className="w-9 h-9 rounded-lg bg-emerald-500/10 flex items-center justify-center">
                        <GitBranch className="h-4 w-4 text-emerald-500" />
                      </div>
                      <div>
                        <h3 className="text-sm font-semibold">{p.name}</h3>
                        <p className="text-xs text-muted-foreground">{p.description}</p>
                      </div>
                    </div>
                    <div className="flex gap-0.5">
                      <Button variant="ghost" size="icon" className="h-7 w-7" onClick={() => openEdit(p)}><Pencil className="h-3 w-3" /></Button>
                      <Button variant="ghost" size="icon" className="h-7 w-7 text-destructive" onClick={() => handleDelete(p.id)}><Trash2 className="h-3 w-3" /></Button>
                    </div>
                  </div>

                  <div className="mb-3">
                    <p className="text-[10px] text-muted-foreground mb-1">流程步骤 ({p.steps.length})</p>
                    <div className="flex items-center gap-1 flex-wrap">
                      {p.steps.map((step, i) => (
                        <span key={step.id} className="flex items-center">
                          <Badge variant="outline" className="text-[10px]">{step.name}</Badge>
                          {i < p.steps.length - 1 && <ArrowRight className="h-2.5 w-2.5 text-muted-foreground/30 mx-0.5" />}
                        </span>
                      ))}
                    </div>
                  </div>

                  <div className="mb-3">
                    <p className="text-[10px] text-muted-foreground mb-1">表单字段 ({p.formFields.length})</p>
                    <div className="flex flex-wrap gap-1">
                      {p.formFields.slice(0, 3).map(f => (
                        <Badge key={f.id} variant="secondary" className="text-[10px]">
                          {f.title}{f.required && <span className="text-destructive ml-0.5">*</span>}
                        </Badge>
                      ))}
                      {p.formFields.length > 3 && (
                        <Badge variant="secondary" className="text-[10px]">+{p.formFields.length - 3}</Badge>
                      )}
                    </div>
                  </div>

                  <div className="flex justify-between items-center pt-2 border-t border-gray-100 dark:border-[rgba(255,255,255,0.04)]">
                    <span className="text-[10px] text-muted-foreground/50">创建于 {p.createdAt}</span>
                    <Button variant="ghost" size="sm" className="h-6 text-xs" onClick={() => openDetail(p)}>
                      <Eye className="h-3 w-3 mr-1" />查看详情
                    </Button>
                  </div>
                </CardContent>
              </Card>
            ))}
          </div>
        )}
      </div>
    )
  }

  // 详情视图 - 全页面
  if (viewMode === 'detail' && currentProcess) {
    return (
      <div className="p-6 space-y-4">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-3">
            <Button variant="ghost" size="icon" className="h-8 w-8" onClick={() => setViewMode('list')}>
              <ChevronLeft className="h-4 w-4" />
            </Button>
            <div>
              <h1 className="text-2xl font-bold">{currentProcess.name}</h1>
              <p className="text-sm text-muted-foreground">{currentProcess.description}</p>
            </div>
          </div>
          <Button onClick={() => openEdit(currentProcess)}>
            <Pencil className="h-4 w-4 mr-1" />编辑
          </Button>
        </div>

        <Tabs defaultValue="form">
          <TabsList>
            <TabsTrigger value="form"><FileText className="h-3 w-3 mr-1" />表单预览</TabsTrigger>
            <TabsTrigger value="flow"><GitBranch className="h-3 w-3 mr-1" />流程步骤</TabsTrigger>
          </TabsList>

          <TabsContent value="form" className="mt-4">
            <Card>
              <CardContent className="p-6">
                {currentProcess.formFields.length === 0 ? (
                  <p className="text-sm text-muted-foreground text-center py-8">暂无表单字段</p>
                ) : (
                  <div className="max-w-3xl">
                    <div className="flex flex-wrap gap-4">
                      {currentProcess.formFields.map(field => {
                        const colWidth = field.colSpan === 6 ? 'w-[calc(50%-8px)]' :
                                         field.colSpan === 4 ? 'w-[calc(33.33%-11px)]' :
                                         field.colSpan === 3 ? 'w-[calc(25%-12px)]' :
                                         'w-full'
                        return (
                          <div key={field.id} className={cn("space-y-1", colWidth)}>
                            <label className="text-sm font-medium">
                              {field.title}
                              {field.required && <span className="text-destructive ml-1">*</span>}
                            </label>
                            {field.type === 'input' && <Input placeholder={field.placeholder} disabled />}
                            {field.type === 'textarea' && <textarea className="flex h-20 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" placeholder={field.placeholder} disabled />}
                            {field.type === 'select' && (
                              <select className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" disabled>
                                <option>{field.placeholder || '请选择'}</option>
                              </select>
                            )}
                            {field.type === 'date' && <Input type="date" disabled />}
                            {field.type === 'number' && <Input type="number" placeholder={field.placeholder} disabled />}
                            {field.type === 'radio' && (
                              <div className="flex items-center gap-4 h-10">
                                <label className="flex items-center gap-1.5"><input type="radio" disabled /><span className="text-sm">选项1</span></label>
                                <label className="flex items-center gap-1.5"><input type="radio" disabled /><span className="text-sm">选项2</span></label>
                              </div>
                            )}
                          </div>
                        )
                      })}
                    </div>
                  </div>
                )}
              </CardContent>
            </Card>
          </TabsContent>

          <TabsContent value="flow" className="mt-4">
            <Card>
              <CardContent className="p-6">
                <div className="space-y-3 max-w-2xl">
                  {currentProcess.steps.map((step, i) => (
                    <div key={step.id} className="flex items-center gap-3">
                      <div className="w-10 h-10 rounded-full bg-emerald-500/10 flex items-center justify-center text-sm font-bold text-emerald-600 shrink-0">
                        {i + 1}
                      </div>
                      <Card className="flex-1">
                        <CardContent className="p-4">
                          <div className="flex items-center justify-between">
                            <div>
                              <p className="text-sm font-medium">{step.name}</p>
                              <p className="text-xs text-muted-foreground">审批人: {step.approver}</p>
                            </div>
                            <div className="flex gap-1">
                              <Badge variant="success" className="text-[10px]">通过 → {step.onSuccess}</Badge>
                              <Badge variant="destructive" className="text-[10px]">拒绝 → {step.onReject}</Badge>
                            </div>
                          </div>
                        </CardContent>
                      </Card>
                      {i < currentProcess.steps.length - 1 && (
                        <ArrowRight className="h-4 w-4 text-muted-foreground/30" />
                      )}
                    </div>
                  ))}
                </div>
              </CardContent>
            </Card>
          </TabsContent>
        </Tabs>
      </div>
    )
  }

  // 创建/编辑视图 - 全页面
  return (
    <div className="p-6 space-y-4">
      {/* 顶部导航 */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <Button variant="ghost" size="icon" className="h-8 w-8" onClick={() => setViewMode('list')}>
            <ChevronLeft className="h-4 w-4" />
          </Button>
          <div>
            <h1 className="text-2xl font-bold">{viewMode === 'create' ? '新建流程' : '编辑流程'}</h1>
            <p className="text-sm text-muted-foreground">设计表单和审批步骤</p>
          </div>
        </div>
        <Button onClick={handleSave} disabled={!form.name}>
          <Save className="h-4 w-4 mr-1" />保存
        </Button>
      </div>

      <Tabs defaultValue="basic">
        <TabsList>
          <TabsTrigger value="basic"><Settings className="h-3 w-3 mr-1" />基本信息</TabsTrigger>
          <TabsTrigger value="form"><FileText className="h-3 w-3 mr-1" />表单设计</TabsTrigger>
          <TabsTrigger value="steps"><GitBranch className="h-3 w-3 mr-1" />流程步骤</TabsTrigger>
        </TabsList>

        {/* 基本信息 */}
        <TabsContent value="basic" className="mt-4">
          <Card>
            <CardContent className="p-6">
              <div className="max-w-xl space-y-4">
                <div>
                  <label className="text-sm font-medium">流程名称 *</label>
                  <Input
                    value={form.name}
                    onChange={e => setForm(f => ({ ...f, name: e.target.value }))}
                    placeholder="标准变更流程"
                  />
                </div>
                <div>
                  <label className="text-sm font-medium">流程描述</label>
                  <textarea
                    className="flex h-20 w-full rounded-md border border-input bg-background px-3 py-2 text-sm"
                    value={form.description}
                    onChange={e => setForm(f => ({ ...f, description: e.target.value }))}
                    placeholder="流程用途说明（可选）"
                  />
                </div>
              </div>
            </CardContent>
          </Card>
        </TabsContent>

        {/* 表单设计 */}
        <TabsContent value="form" className="mt-4">
          <Card>
            <CardContent className="p-4">
              <FormDesigner value={formFields} onChange={setFormFields} />
            </CardContent>
          </Card>
        </TabsContent>

        {/* 流程步骤 */}
        <TabsContent value="steps" className="mt-4">
          <Card>
            <CardHeader className="py-3">
              <div className="flex items-center justify-between">
                <CardTitle className="text-sm">审批步骤</CardTitle>
                <Button size="sm" onClick={addStep}><Plus className="h-3 w-3 mr-1" />添加步骤</Button>
              </div>
            </CardHeader>
            <CardContent>
              {steps.length === 0 ? (
                <p className="text-sm text-muted-foreground text-center py-8">暂无步骤，点击"添加步骤"创建</p>
              ) : (
                <div className="space-y-3">
                  {steps.map((step, i) => (
                    <div key={step.id} className="flex items-start gap-3 p-3 rounded-lg border border-gray-100 dark:border-[rgba(255,255,255,0.04)]">
                      <div className="w-8 h-8 rounded-full bg-emerald-500/10 flex items-center justify-center text-xs font-bold text-emerald-600 shrink-0 mt-1">
                        {i + 1}
                      </div>
                      <div className="flex-1 grid grid-cols-2 gap-3">
                        <div>
                          <label className="text-xs text-muted-foreground">步骤名称</label>
                          <Input
                            className="h-8 text-xs mt-1"
                            value={step.name}
                            onChange={e => updateStep(step.id, { name: e.target.value })}
                          />
                        </div>
                        <div>
                          <label className="text-xs text-muted-foreground">审批人</label>
                          <Input
                            className="h-8 text-xs mt-1"
                            value={step.approver}
                            onChange={e => updateStep(step.id, { approver: e.target.value })}
                            placeholder="角色或用户名"
                          />
                        </div>
                        <div>
                          <label className="text-xs text-muted-foreground">通过后流转</label>
                          <Input
                            className="h-8 text-xs mt-1"
                            value={step.onSuccess}
                            onChange={e => updateStep(step.id, { onSuccess: e.target.value })}
                            placeholder="下一步骤名称"
                          />
                        </div>
                        <div>
                          <label className="text-xs text-muted-foreground">拒绝后流转</label>
                          <Input
                            className="h-8 text-xs mt-1"
                            value={step.onReject}
                            onChange={e => updateStep(step.id, { onReject: e.target.value })}
                            placeholder="退回步骤"
                          />
                        </div>
                      </div>
                      <Button
                        variant="ghost"
                        size="icon"
                        className="h-8 w-8 text-destructive shrink-0"
                        onClick={() => deleteStep(step.id)}
                      >
                        <Trash2 className="h-3 w-3" />
                      </Button>
                    </div>
                  ))}
                </div>
              )}
            </CardContent>
          </Card>
        </TabsContent>
      </Tabs>
    </div>
  )
}
