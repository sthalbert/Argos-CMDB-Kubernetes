import { Logomark } from './Logomark';

export function Brand({ size = 26 }: { size?: number }) {
  return (
    <div className="lv-brand">
      <Logomark size={size} className="lv-brand-mark" />
      <div className="lv-brand-name">
        Longue<em>·</em>vue
      </div>
    </div>
  );
}
