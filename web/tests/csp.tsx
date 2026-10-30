import { useEffect, useRef, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { Badge, Button, Checkbox, Heading, SegmentedControl, Text, TextField, Theme } from '@radix-ui/themes';
import '@radix-ui/themes/styles.css';
import './csp.css';

function Fixture() {
  const dialog = useRef<HTMLDialogElement>(null);
  const launcher = useRef<HTMLButtonElement>(null);
  const menu = useRef<HTMLDetailsElement>(null);
  const [appearance, setAppearance] = useState<'light' | 'dark'>('light');
  useEffect(() => {
    const media = matchMedia('(prefers-color-scheme: dark)');
    const update = () => setAppearance(media.matches ? 'dark' : 'light');
    update();
    media.addEventListener('change', update);
    return () => media.removeEventListener('change', update);
  }, []);
  return <Theme appearance={appearance} accentColor="blue" radius="medium">
    <Heading>组件兼容性</Heading>
    <Text data-testid="appearance">{appearance}</Text>
    <TextField.Root aria-label="搜索资产" placeholder="搜索资产" />
    <Badge color="red">有风险</Badge>
    <Text as="label"><Checkbox /> 已审阅</Text>
    <SegmentedControl.Root aria-label="筛选状态" defaultValue="all"><SegmentedControl.Item value="all">全部</SegmentedControl.Item><SegmentedControl.Item value="risk">有风险</SegmentedControl.Item></SegmentedControl.Root>
    <details ref={menu} onKeyDown={event => {
      if (event.key === 'Escape' && menu.current?.open) {
        menu.current.open = false;
        menu.current.querySelector('summary')?.focus();
      }
    }}><summary>会话菜单</summary><Button>菜单项目</Button></details>
    <Button ref={launcher} onClick={() => dialog.current?.showModal()}>打开详情</Button>
    <dialog ref={dialog} onClose={() => launcher.current?.focus()} aria-labelledby="detail-title"><Heading id="detail-title">详情</Heading><Text>完整信息</Text><Button onClick={() => dialog.current?.close()}>关闭</Button></dialog>
  </Theme>;
}
createRoot(document.getElementById('csp-root')!).render(<Fixture />);
