import { describe, it, expect } from 'vitest';
import { fireEvent } from '@testing-library/react';
import { renderWithRouter } from '../../test/render';
import { Disclosure } from './Disclosure';

const Trigger = ({ open, toggle }: { open: boolean; toggle: () => void }) => (
  <button onClick={toggle} aria-expanded={open}>Menu</button>
);
const Body = () => <div data-testid="popover">items</div>;

describe('Disclosure', () => {
  it('starts closed', () => {
    const { queryByTestId } = renderWithRouter(<Disclosure trigger={Trigger}>{Body}</Disclosure>);
    expect(queryByTestId('popover')).toBeNull();
  });

  it('opens on trigger click', () => {
    const { getByText, queryByTestId } = renderWithRouter(<Disclosure trigger={Trigger}>{Body}</Disclosure>);
    fireEvent.click(getByText('Menu'));
    expect(queryByTestId('popover')).not.toBeNull();
  });

  it('closes on outside click', () => {
    const { getByText, queryByTestId } = renderWithRouter(
      <div>
        <Disclosure trigger={Trigger}>{Body}</Disclosure>
        <span data-testid="outside">x</span>
      </div>,
    );
    fireEvent.click(getByText('Menu'));
    expect(queryByTestId('popover')).not.toBeNull();
    fireEvent.mouseDown(document.querySelector('[data-testid="outside"]')!);
    expect(queryByTestId('popover')).toBeNull();
  });

  it('closes on ESC', () => {
    const { getByText, queryByTestId } = renderWithRouter(<Disclosure trigger={Trigger}>{Body}</Disclosure>);
    fireEvent.click(getByText('Menu'));
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(queryByTestId('popover')).toBeNull();
  });

  it('exposes a render prop for closing programmatically', () => {
    const { getByText, queryByTestId } = renderWithRouter(
      <Disclosure trigger={Trigger}>
        {({ close }) => <button onClick={close} data-testid="popover">close-me</button>}
      </Disclosure>,
    );
    fireEvent.click(getByText('Menu'));
    fireEvent.click(getByText('close-me'));
    expect(queryByTestId('popover')).toBeNull();
  });
});
