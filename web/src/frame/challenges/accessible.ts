import type { ChallengeSpec, ChallengeView, Trace } from '../types';
import type { Translator } from '../i18n';
import { el } from '../ui';

/**
 * The non-visual fallback: a short question with four choices.
 *
 * This exists because a slider, a dial and a drag target are all unusable
 * with a screen reader, and largely unusable for anyone with a motor
 * impairment that makes fine dragging hard. Offering only pointer puzzles
 * would lock those visitors out of every site that deploys this service,
 * which is not an acceptable trade for any amount of bot resistance.
 *
 * It is scored on the answer and the proof of work alone, with no motion
 * requirement, because demanding a human-looking gesture here would defeat
 * the entire purpose.
 *
 * The question text is assembled in the browser from translation keys, so the
 * Persian version reads naturally rather than being a transliteration of an
 * English sentence.
 */
export class AccessibleChallenge implements ChallengeView {
  onChange?: (ready: boolean) => void;

  private choice: string | null = null;
  private readonly name = `pc-choice-${Math.random().toString(36).slice(2, 8)}`;
  private readonly cleanups: Array<() => void> = [];

  constructor(
    private readonly spec: ChallengeSpec,
    private readonly t: Translator,
  ) {}

  mount(parent: HTMLElement): void {
    const a = this.spec.accessible!;

    const group = el('fieldset', 'pc-quiz');
    const legend = el('legend', 'pc-prompt');
    legend.textContent = this.t.t(a.promptKey, a.args as Record<string, number> | undefined);
    group.append(legend);

    for (const option of a.options) {
      const label = el('label', 'pc-choice');
      const input = el('input');
      input.type = 'radio';
      input.name = this.name;
      input.value = option.id;

      const text =
        option.labelKey !== undefined
          ? this.t.t(option.labelKey)
          : this.t.num(option.number ?? 0);

      const onChange = () => {
        this.choice = option.id;
        this.onChange?.(true);
      };
      input.addEventListener('change', onChange);
      this.cleanups.push(() => input.removeEventListener('change', onChange));

      label.append(input, el('span', 'pc-choice-text', text));
      group.append(label);
    }

    const hint = el('p', 'pc-hint', this.t.t('access.hint'));
    parent.append(group, hint);
  }

  destroy(): void {
    for (const off of this.cleanups) off();
    this.cleanups.length = 0;
  }

  ready(): boolean {
    return this.choice !== null;
  }

  answer(): unknown {
    return { choice: this.choice };
  }

  /**
   * There is no pointer motion to report. The mode is declared as keyboard so
   * the server applies its keyboard scoring path, which does not expect the
   * jitter a mouse would produce.
   */
  trace(): Trace {
    return { mode: 'keyboard', points: [], startT: 0, endT: 0, corrections: 0 };
  }
}
