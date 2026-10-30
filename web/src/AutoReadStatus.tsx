import { Button, Checkbox } from '@radix-ui/themes';
import type { AutoReadState } from './useQuery';

export function AutoReadStatus({ state }: { state: AutoReadState }) {
  return <div data-auto-read data-reading={state.reading} data-failures={state.failures}>
    <label className="reviewed"><Checkbox checked={state.enabled} onCheckedChange={value => state.setEnabled(value === true)} /> 自动重读（60 秒）</label>
    <p className="muted" role="status">{state.reading ? '正在重读已保存结果… ' : ''}{state.readAt && <>页面读取时间：<time>{state.readAt}</time>。</>} 本页不会触发采集。</p>
    {state.pendingCount > 0 && <Button size="1" variant="soft" onClick={state.apply}>有 {state.pendingCount} 项更新，应用更新</Button>}
    {state.error && <p className="notice" role="alert">重读失败，保留上次内容。最后成功时间：{state.readAt}。可使用“重新读取”重试。{state.failures >= 3 && ' 下列状态可能已过期。'}</p>}
  </div>;
}
