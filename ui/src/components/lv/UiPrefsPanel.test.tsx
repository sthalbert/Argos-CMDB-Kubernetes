import { describe, it, expect } from 'vitest';
import { render, fireEvent } from '@testing-library/react';
import { UiPrefsProvider } from '../../ui-prefs';
import { UiPrefsPanel } from './UiPrefsPanel';

describe('UiPrefsPanel', () => {
  it('renders three sections with radios', () => {
    const { container } = render(<UiPrefsProvider><UiPrefsPanel /></UiPrefsProvider>);
    const radios = container.querySelectorAll('input[type="radio"]');
    // 5 accents + 3 densities + 3 pill styles
    expect(radios.length).toBe(11);
  });

  it('clicking a radio updates body dataset', () => {
    const { getByLabelText } = render(<UiPrefsProvider><UiPrefsPanel /></UiPrefsProvider>);
    fireEvent.click(getByLabelText('amber'));
    expect(document.body.dataset.accent).toBe('amber');
  });

  it('marks the current pref as checked', () => {
    localStorage.setItem('lv:ui-prefs', JSON.stringify({ accent: 'sage', density: 'standard', pillStyle: 'dot' }));
    const { getByLabelText } = render(<UiPrefsProvider><UiPrefsPanel /></UiPrefsProvider>);
    expect((getByLabelText('sage') as HTMLInputElement).checked).toBe(true);
    expect((getByLabelText('dot') as HTMLInputElement).checked).toBe(true);
  });
});
