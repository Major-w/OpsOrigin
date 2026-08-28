
import React, { useState, useRef, useCallback } from 'react';
import { GripVertical, Trash2, Copy, Settings } from 'lucide-react';

export interface DragItem {
  id: string;
  type: string;
  label: string;
  props: Record<string, any>;
  children?: DragItem[];
  sortOrder: number;
}

interface DragDropContainerProps {
  items: DragItem[];
  onItemsChange: (items: DragItem[]) => void;
  onItemSelect?: (item: DragItem | null) => void;
  className?: string;
}

interface DragDropItemProps {
  item: DragItem;
  index: number;
  isSelected: boolean;
  onSelect: () => void;
  onDragStart: (index: number) => void;
  onDragOver: (index: number) => void;
  onDragEnd: () => void;
  onDelete: () => void;
  onCopy: () => void;
}

const DragDropItem: React.FC<DragDropItemProps> = ({
  item,
  index,
  isSelected,
  onSelect,
  onDragStart,
  onDragOver,
  onDragEnd,
  onDelete,
  onCopy,
}) => {
  const [isDragging, setIsDragging] = useState(false);

  const handleDragStart = (e: React.DragEvent) => {
    setIsDragging(true);
    onDragStart(index);
    e.dataTransfer.effectAllowed = 'move';
    e.dataTransfer.setData('text/plain', index.toString());
  };

  const handleDragEnd = () => {
    setIsDragging(false);
    onDragEnd();
  };

  const handleDragOver = (e: React.DragEvent) => {
    e.preventDefault();
    e.dataTransfer.dropEffect = 'move';
    onDragOver(index);
  };

  const renderComponent = () => {
    switch (item.type) {
      case 'button':
        return (
          <button
            className={`px-4 py-2 rounded ${item.props.variant === 'primary' ? 'bg-blue-500 text-white' : 'bg-gray-200'}`}
            disabled={item.props.disabled}
          >
            {item.props.text || item.label}
          </button>
        );
      case 'input':
        return (
          <input
            type={item.props.type || 'text'}
            placeholder={item.props.placeholder}
            className="px-3 py-2 border rounded w-full"
            disabled={item.props.disabled}
          />
        );
      case 'select':
        return (
          <select
            className="px-3 py-2 border rounded w-full"
            disabled={item.props.disabled}
          >
            <option value="">{item.props.placeholder || '请选择'}</option>
            {(item.props.options || []).map((opt: any, i: number) => (
              <option key={i} value={opt.value}>{opt.label}</option>
            ))}
          </select>
        );
      case 'textarea':
        return (
          <textarea
            placeholder={item.props.placeholder}
            className="px-3 py-2 border rounded w-full"
            rows={item.props.rows || 3}
            disabled={item.props.disabled}
          />
        );
      case 'checkbox':
        return (
          <label className="flex items-center gap-2">
            <input
              type="checkbox"
              checked={item.props.checked}
              disabled={item.props.disabled}
            />
            <span>{item.props.label || item.label}</span>
          </label>
        );
      case 'radio':
        return (
          <div className="flex flex-col gap-2">
            {(item.props.options || []).map((opt: any, i: number) => (
              <label key={i} className="flex items-center gap-2">
                <input
                  type="radio"
                  name={item.id}
                  value={opt.value}
                  checked={item.props.value === opt.value}
                  disabled={item.props.disabled}
                />
                <span>{opt.label}</span>
              </label>
            ))}
          </div>
        );
      case 'switch':
        return (
          <label className="flex items-center gap-2">
            <div className={`w-10 h-5 rounded-full ${item.props.checked ? 'bg-blue-500' : 'bg-gray-300'}`}>
              <div className={`w-4 h-4 rounded-full bg-white transform transition-transform ${item.props.checked ? 'translate-x-5' : 'translate-x-0'}`} />
            </div>
            <span>{item.props.label || item.label}</span>
          </label>
        );
      case 'datepicker':
        return (
          <input
            type="date"
            className="px-3 py-2 border rounded"
            disabled={item.props.disabled}
          />
        );
      case 'timepicker':
        return (
          <input
            type="time"
            className="px-3 py-2 border rounded"
            disabled={item.props.disabled}
          />
        );
      case 'upload':
        return (
          <div className="border-2 border-dashed rounded p-4 text-center">
            <p className="text-gray-500">点击或拖拽文件到此处上传</p>
          </div>
        );
      case 'grid':
        return (
          <div className="grid grid-cols-2 gap-4 p-4 border rounded bg-gray-50">
            <div className="h-20 border rounded bg-white flex items-center justify-center text-gray-400">列 1</div>
            <div className="h-20 border rounded bg-white flex items-center justify-center text-gray-400">列 2</div>
          </div>
        );
      case 'tabs':
        return (
          <div className="border rounded">
            <div className="flex border-b">
              <div className="px-4 py-2 border-b-2 border-blue-500">标签 1</div>
              <div className="px-4 py-2 text-gray-500">标签 2</div>
            </div>
            <div className="p-4">标签内容</div>
          </div>
        );
      case 'table':
        return (
          <div className="border rounded">
            <table className="w-full">
              <thead className="bg-gray-50">
                <tr>
                  <th className="px-4 py-2 text-left">列 1</th>
                  <th className="px-4 py-2 text-left">列 2</th>
                </tr>
              </thead>
              <tbody>
                <tr className="border-t">
                  <td className="px-4 py-2">数据 1</td>
                  <td className="px-4 py-2">数据 2</td>
                </tr>
              </tbody>
            </table>
          </div>
        );
      case 'modal':
        return (
          <div className="border rounded p-4">
            <div className="flex justify-between items-center mb-4">
              <h3 className="font-medium">{item.props.title || '对话框'}</h3>
              <button className="text-gray-400">×</button>
            </div>
            <div className="text-gray-500">对话框内容</div>
          </div>
        );
      case 'tree':
        return (
          <div className="border rounded p-4">
            <div className="flex items-center gap-2">
              <span>📁</span>
              <span>根节点</span>
            </div>
            <div className="ml-6 mt-2">
              <div className="flex items-center gap-2">
                <span>📄</span>
                <span>子节点 1</span>
              </div>
              <div className="flex items-center gap-2 mt-1">
                <span>📄</span>
                <span>子节点 2</span>
              </div>
            </div>
          </div>
        );
      default:
        return (
          <div className="p-4 border rounded bg-gray-50 text-center text-gray-500">
            {item.label}
          </div>
        );
    }
  };

  return (
    <div
      className={`relative group p-2 mb-2 rounded border-2 transition-colors ${
        isDragging ? 'border-blue-500 bg-blue-50' : 
        isSelected ? 'border-blue-500 bg-blue-50' : 
        'border-transparent hover:border-gray-300'
      }`}
      draggable
      onDragStart={handleDragStart}
      onDragEnd={handleDragEnd}
      onDragOver={handleDragOver}
      onClick={(e) => {
        e.stopPropagation();
        onSelect();
      }}
    >
      <div className="absolute left-0 top-0 bottom-0 w-6 flex items-center justify-center cursor-move opacity-0 group-hover:opacity-100 transition-opacity">
        <GripVertical className="w-4 h-4 text-gray-400" />
      </div>

      <div className="absolute right-2 top-2 flex gap-1 opacity-0 group-hover:opacity-100 transition-opacity">
        <button
          onClick={(e) => {
            e.stopPropagation();
            onCopy();
          }}
          className="p-1 rounded hover:bg-gray-200"
          title="复制"
        >
          <Copy className="w-3 h-3" />
        </button>
        <button
          onClick={(e) => {
            e.stopPropagation();
            onDelete();
          }}
          className="p-1 rounded hover:bg-red-100 text-red-500"
          title="删除"
        >
          <Trash2 className="w-3 h-3" />
        </button>
      </div>

      <div className="pl-6">
        {renderComponent()}
      </div>
    </div>
  );
};

export const DragDropContainer: React.FC<DragDropContainerProps> = ({
  items,
  onItemsChange,
  onItemSelect,
  className = '',
}) => {
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const dragIndexRef = useRef<number | null>(null);

  const handleDragStart = useCallback((index: number) => {
    dragIndexRef.current = index;
  }, []);

  const handleDragOver = useCallback((index: number) => {
    if (dragIndexRef.current === null || dragIndexRef.current === index) {
      return;
    }

    const newItems = [...items];
    const draggedItem = newItems[dragIndexRef.current];
    newItems.splice(dragIndexRef.current, 1);
    newItems.splice(index, 0, draggedItem);

    newItems.forEach((item, i) => {
      item.sortOrder = i;
    });

    onItemsChange(newItems);
    dragIndexRef.current = index;
  }, [items, onItemsChange]);

  const handleDragEnd = useCallback(() => {
    dragIndexRef.current = null;
  }, []);

  const handleSelect = useCallback((item: DragItem | null) => {
    setSelectedId(item?.id || null);
    onItemSelect?.(item);
  }, [onItemSelect]);

  const handleDelete = useCallback((id: string) => {
    const newItems = items.filter(item => item.id !== id);
    newItems.forEach((item, i) => {
      item.sortOrder = i;
    });
    onItemsChange(newItems);
    if (selectedId === id) {
      setSelectedId(null);
      onItemSelect?.(null);
    }
  }, [items, onItemsChange, selectedId, onItemSelect]);

  const handleCopy = useCallback((item: DragItem) => {
    const newItem: DragItem = {
      ...item,
      id: `${item.id}-copy-${Date.now()}`,
      label: `${item.label} (副本)`,
      sortOrder: items.length,
    };
    onItemsChange([...items, newItem]);
  }, [items, onItemsChange]);

  return (
    <div className={`drag-drop-container ${className}`}>
      {items.length === 0 ? (
        <div className="p-8 border-2 border-dashed rounded text-center text-gray-500">
          从左侧组件库拖拽组件到此处
        </div>
      ) : (
        items.map((item, index) => (
          <DragDropItem
            key={item.id}
            item={item}
            index={index}
            isSelected={selectedId === item.id}
            onSelect={() => handleSelect(item)}
            onDragStart={handleDragStart}
            onDragOver={handleDragOver}
            onDragEnd={handleDragEnd}
            onDelete={() => handleDelete(item.id)}
            onCopy={() => handleCopy(item)}
          />
        ))
      )}
    </div>
  );
};

interface ComponentLibraryProps {
  onAddComponent: (component: Omit<DragItem, 'id' | 'sortOrder'>) => void;
}

export const ComponentLibrary: React.FC<ComponentLibraryProps> = ({ onAddComponent }) => {
  const components = [
    { type: 'button', label: '按钮', icon: '🔘', category: '基础' },
    { type: 'input', label: '输入框', icon: '📝', category: '基础' },
    { type: 'select', label: '下拉选择', icon: '📋', category: '基础' },
    { type: 'textarea', label: '文本域', icon: '📄', category: '基础' },
    { type: 'checkbox', label: '复选框', icon: '☑️', category: '表单' },
    { type: 'radio', label: '单选框', icon: '🔘', category: '表单' },
    { type: 'switch', label: '开关', icon: '🔀', category: '表单' },
    { type: 'datepicker', label: '日期选择', icon: '📅', category: '表单' },
    { type: 'timepicker', label: '时间选择', icon: '🕐', category: '表单' },
    { type: 'upload', label: '文件上传', icon: '📤', category: '表单' },
    { type: 'grid', label: '栅格布局', icon: '⊞', category: '布局' },
    { type: 'tabs', label: '标签页', icon: '📂', category: '布局' },
    { type: 'table', label: '表格', icon: '📊', category: '高级' },
    { type: 'modal', label: '对话框', icon: '💬', category: '高级' },
    { type: 'tree', label: '树形控件', icon: '🌳', category: '高级' },
  ];

  const groupedComponents = components.reduce((acc, comp) => {
    if (!acc[comp.category]) {
      acc[comp.category] = [];
    }
    acc[comp.category].push(comp);
    return acc;
  }, {} as Record<string, typeof components>);

  const handleDragStart = (e: React.DragEvent, component: typeof components[0]) => {
    e.dataTransfer.setData('component', JSON.stringify(component));
    e.dataTransfer.effectAllowed = 'copy';
  };

  return (
    <div className="component-library border-r p-4 w-64">
      <h3 className="font-medium mb-4">组件库</h3>
      {Object.entries(groupedComponents).map(([category, comps]) => (
        <div key={category} className="mb-4">
          <h4 className="text-sm font-medium text-gray-500 mb-2">{category}</h4>
          <div className="grid grid-cols-2 gap-2">
            {comps.map((comp) => (
              <div
                key={comp.type}
                className="p-2 border rounded cursor-pointer hover:bg-gray-50 flex flex-col items-center gap-1"
                draggable
                onDragStart={(e) => handleDragStart(e, comp)}
                onClick={() => onAddComponent({
                  type: comp.type,
                  label: comp.label,
                  props: {},
                })}
              >
                <span className="text-lg">{comp.icon}</span>
                <span className="text-xs">{comp.label}</span>
              </div>
            ))}
          </div>
        </div>
      ))}
    </div>
  );
};

interface PropertyPanelProps {
  item: DragItem | null;
  onChange: (props: Record<string, any>) => void;
}

export const PropertyPanel: React.FC<PropertyPanelProps> = ({ item, onChange }) => {
  if (!item) {
    return (
      <div className="property-panel border-l p-4 w-64">
        <p className="text-gray-500 text-center">请选择一个组件</p>
      </div>
    );
  }

  const handleChange = (key: string, value: any) => {
    onChange({ ...item.props, [key]: value });
  };

  return (
    <div className="property-panel border-l p-4 w-64">
      <h3 className="font-medium mb-4">属性配置</h3>
      <div className="space-y-4">
        <div>
          <label className="block text-sm font-medium text-gray-700 mb-1">组件类型</label>
          <input
            type="text"
            value={item.type}
            disabled
            className="w-full px-3 py-2 border rounded bg-gray-50"
          />
        </div>
        <div>
          <label className="block text-sm font-medium text-gray-700 mb-1">标签</label>
          <input
            type="text"
            value={item.label}
            onChange={(e) => handleChange('label', e.target.value)}
            className="w-full px-3 py-2 border rounded"
          />
        </div>
        {item.type === 'button' && (
          <>
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">按钮文本</label>
              <input
                type="text"
                value={item.props.text || ''}
                onChange={(e) => handleChange('text', e.target.value)}
                className="w-full px-3 py-2 border rounded"
              />
            </div>
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">样式</label>
              <select
                value={item.props.variant || 'default'}
                onChange={(e) => handleChange('variant', e.target.value)}
                className="w-full px-3 py-2 border rounded"
              >
                <option value="default">默认</option>
                <option value="primary">主要</option>
                <option value="danger">危险</option>
              </select>
            </div>
          </>
        )}
        {item.type === 'input' && (
          <>
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">输入类型</label>
              <select
                value={item.props.type || 'text'}
                onChange={(e) => handleChange('type', e.target.value)}
                className="w-full px-3 py-2 border rounded"
              >
                <option value="text">文本</option>
                <option value="password">密码</option>
                <option value="number">数字</option>
                <option value="email">邮箱</option>
              </select>
            </div>
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">占位符</label>
              <input
                type="text"
                value={item.props.placeholder || ''}
                onChange={(e) => handleChange('placeholder', e.target.value)}
                className="w-full px-3 py-2 border rounded"
              />
            </div>
          </>
        )}
        <div>
          <label className="block text-sm font-medium text-gray-700 mb-1">禁用</label>
          <input
            type="checkbox"
            checked={item.props.disabled || false}
            onChange={(e) => handleChange('disabled', e.target.checked)}
          />
        </div>
      </div>
    </div>
  );
};
