import { Button, TextField } from '@radix-ui/themes';
import type { PageProps } from './scope';

export function ScopeBar({ scope, onScopeChange, onRefresh }: Pick<PageProps, 'scope' | 'onScopeChange'> & { onRefresh: () => void }) {
  return <form className="scope-bar" onSubmit={event => {
    event.preventDefault();
    const days = Number(new FormData(event.currentTarget).get('days'));
    if (Number.isSafeInteger(days) && days >= 0) onScopeChange({ ...scope, days });
  }}>
    <label htmlFor="days">证据有效期（天）</label><TextField.Root key={scope.days} id="days" name="days" type="number" min="0" step="1" required defaultValue={String(scope.days)} size="1" />
    <Button type="submit" size="1" variant="soft">应用有效期</Button><Button type="button" size="1" variant="outline" onClick={onRefresh}>重新读取</Button>
  </form>;
}
