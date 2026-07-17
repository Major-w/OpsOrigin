'use client'

import React, { useState, useEffect } from 'react';
import { Eye, EyeOff, Maximize2, Minimize2, RefreshCw } from 'lucide-react';

interface PreviewComponent {
  id: string;
  type: string;
  label: string;
  props: Record<string, any>;
  children?: PreviewComponent[];
}

interface LivePreviewProps {
  components: PreviewComponent[];
  className?: string;
  showBorder?: boolean;
  refreshInterval?: number;
}

export const LivePreview: React.FC<LivePreviewProps> = ({
  components,
  className = '',
  showBorder = true,
  refreshInterval = 0,
}) => {
  const [isFullscreen, setIsFullscreen] = useState(false);
  const [showBorders, setShowBorders] = useState(showBorder);
  const [refreshKey, setRefreshKey] = useState(0);

  useEffect(() => {
    if (refreshInterval > 0) {
      const timer = setInterval(() => {
        setRefreshKey(k => k + 1);
      }, refreshInterval);
      return () => clearInterval(timer);
    }
  }, [refreshInterval]);

  const renderComponent = (component: PreviewComponent) => {
    const { type, props, children } = component;

    switch (type) {
      case 'button':
        return (
          <button
            className={`px-4 py-2 rounded ${
              props.variant === 'primary' ? 'bg-blue-500 text-white' : 
              props.variant === 'danger' ? 'bg-red-500 text-white' : 
              'bg-gray-200'
            } ${props.disabled ? 'opacity-50 cursor-not-allowed' : ''}`}
            disabled={props.disabled}
          >
            {props.text || component.label}
          </button>
        );

      case 'input':
        return (
          <input
            type={props.type || 'text'}
            placeholder={props.placeholder}
            className={`px-3 py-2 border rounded w-full ${props.disabled ? 'bg-gray-50' : ''}`}
            disabled={props.disabled}
          />
        );

      case 'select':
        return (
          <select
            className={`px-3 py-2 border rounded w-full ${props.disabled ? 'bg-gray-50' : ''}`}
            disabled={props.disabled}
          >
            <option value="">{props.placeholder || '请选择'}</option>
            {(props.options || []).map((opt: any, i: number) => (
              <option key={i} value={opt.value}>{opt.label}</option>
            ))}
          </select>
        );

      case 'textarea':
        return (
          <textarea
            placeholder={props.placeholder}
            className={`px-3 py-2 border rounded w-full ${props.disabled ? 'bg-gray-50' : ''}`}
            rows={props.rows || 3}
            disabled={props.disabled}
          />
        );

      case 'checkbox':
        return (
          <label className="flex items-center gap-2">
            <input
              type="checkbox"
              checked={props.checked}
              disabled={props.disabled}
            />
            <span>{props.label || component.label}</span>
          </label>
        );

      case 'radio':
        return (
          <div className="flex flex-col gap-2">
            {(props.options || []).map((opt: any, i: number) => (
              <label key={i} className="flex items-center gap-2">
                <input
                  type="radio"
                  name={component.id}
                  value={opt.value}
                  checked={props.value === opt.value}
                  disabled={props.disabled}
                />
                <span>{opt.label}</span>
              </label>
            ))}
          </div>
        );

      case 'switch':
        return (
          <label className="flex items-center gap-2">
            <div className={`w-10 h-5 rounded-full ${props.checked ? 'bg-blue-500' : 'bg-gray-300'}`}>
              <div className={`w-4 h-4 rounded-full bg-white transform transition-transform ${props.checked ? 'translate-x-5' : 'translate-x-0'}`} />
            </div>
            <span>{props.label || component.label}</span>
          </label>
        );

      case 'datepicker':
        return (
          <input
            type="date"
            className={`px-3 py-2 border rounded ${props.disabled ? 'bg-gray-50' : ''}`}
            disabled={props.disabled}
          />
        );

      case 'timepicker':
        return (
          <input
            type="time"
            className={`px-3 py-2 border rounded ${props.disabled ? 'bg-gray-50' : ''}`}
            disabled={props.disabled}
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
          <div className="grid grid-cols-2 gap-4">
            {children?.map(child => (
              <div key={child.id} className={`p-4 border rounded ${showBorders ? 'border-dashed border-gray-300' : ''}`}>
                {renderComponent(child)}
              </div>
            )) || (
              <>
                <div className="h-20 border rounded bg-gray-50 flex items-center justify-center text-gray-400">列 1</div>
                <div className="h-20 border rounded bg-gray-50 flex items-center justify-center text-gray-400">列 2</div>
              </>
            )}
          </div>
        );

      case 'tabs':
        return (
          <div className="border rounded">
            <div className="flex border-b">
              <div className="px-4 py-2 border-b-2 border-blue-500">标签 1</div>
              <div className="px-4 py-2 text-gray-500">标签 2</div>
            </div>
            <div className="p-4">
              {children?.map(child => (
                <div key={child.id}>{renderComponent(child)}</div>
              )) || '标签内容'}
            </div>
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
              <h3 className="font-medium">{props.title || '对话框'}</h3>
              <button className="text-gray-400">×</button>
            </div>
            <div className="text-gray-500">
              {children?.map(child => (
                <div key={child.id}>{renderComponent(child)}</div>
              )) || '对话框内容'}
            </div>
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
            {component.label}
          </div>
        );
    }
  };

  return (
    <div className={`live-preview ${isFullscreen ? 'fixed inset-0 z-50 bg-white' : ''} ${className}`}>
      <div className="flex items-center justify-between p-2 border-b bg-gray-50">
        <div className="flex items-center gap-2">
          <Eye className="w-4 h-4 text-blue-500" />
          <span className="text-sm font-medium">实时预览</span>
        </div>
        <div className="flex items-center gap-1">
          <button
            onClick={() => setShowBorders(!showBorders)}
            className={`p-1 rounded ${showBorders ? 'bg-blue-100 text-blue-600' : 'hover:bg-gray-200'}`}
            title={showBorders ? '隐藏边框' : '显示边框'}
          >
            {showBorders ? <Eye className="w-4 h-4" /> : <EyeOff className="w-4 h-4" />}
          </button>
          <button
            onClick={() => setRefreshKey(k => k + 1)}
            className="p-1 rounded hover:bg-gray-200"
            title="刷新预览"
          >
            <RefreshCw className="w-4 h-4" />
          </button>
          <button
            onClick={() => setIsFullscreen(!isFullscreen)}
            className="p-1 rounded hover:bg-gray-200"
            title={isFullscreen ? '退出全屏' : '全屏预览'}
          >
            {isFullscreen ? <Minimize2 className="w-4 h-4" /> : <Maximize2 className="w-4 h-4" />}
          </button>
        </div>
      </div>

      <div 
        key={refreshKey}
        className={`p-4 overflow-auto ${isFullscreen ? 'h-[calc(100vh-48px)]' : 'max-h-[600px]'}`}
      >
        {components.length === 0 ? (
          <div className="text-center py-12 text-gray-500">
            暂无组件，请从组件库添加
          </div>
        ) : (
          <div className="space-y-4">
            {components.map(component => (
              <div
                key={component.id}
                className={`p-2 ${showBorders ? 'border border-dashed border-gray-200 rounded' : ''}`}
              >
                {renderComponent(component)}
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
};

interface ResponsivePreviewProps {
  components: PreviewComponent[];
  className?: string;
}

export const ResponsivePreview: React.FC<ResponsivePreviewProps> = ({
  components,
  className = '',
}) => {
  const [viewport, setViewport] = useState<'desktop' | 'tablet' | 'mobile'>('desktop');

  const viewportWidths = {
    desktop: '100%',
    tablet: '768px',
    mobile: '375px',
  };

  return (
    <div className={`responsive-preview ${className}`}>
      <div className="flex items-center justify-center gap-2 p-2 border-b bg-gray-50">
        <button
          onClick={() => setViewport('desktop')}
          className={`px-3 py-1 text-sm rounded ${viewport === 'desktop' ? 'bg-blue-100 text-blue-600' : 'hover:bg-gray-200'}`}
        >
          桌面端
        </button>
        <button
          onClick={() => setViewport('tablet')}
          className={`px-3 py-1 text-sm rounded ${viewport === 'tablet' ? 'bg-blue-100 text-blue-600' : 'hover:bg-gray-200'}`}
        >
          平板
        </button>
        <button
          onClick={() => setViewport('mobile')}
          className={`px-3 py-1 text-sm rounded ${viewport === 'mobile' ? 'bg-blue-100 text-blue-600' : 'hover:bg-gray-200'}`}
        >
          手机
        </button>
      </div>

      <div className="flex justify-center p-4 bg-gray-100">
        <div
          className="bg-white border rounded shadow-sm overflow-auto"
          style={{
            width: viewportWidths[viewport],
            maxWidth: '100%',
            height: viewport === 'mobile' ? '667px' : viewport === 'tablet' ? '1024px' : 'auto',
            maxHeight: '800px',
          }}
        >
          <LivePreview components={components} showBorder={false} />
        </div>
      </div>
    </div>
  );
};
