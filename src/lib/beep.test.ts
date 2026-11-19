import { afterEach, describe, expect, it, vi } from 'vitest';
import { createBeeper, type Tone } from './beep';

type Resume = 'ok' | 'never' | 'fail';

/** An audio context that records the tones it plays; iOS without a tap never resumes. */
function fakeAudio(state: AudioContextState, resume: Resume = 'ok', endsTone = true) {
	const fake = {
		state,
		currentTime: 2,
		destination: {},
		tones: [] as { at: number; frequency: number }[],
		suspends: 0,
		resumes: 0,
		resume() {
			fake.resumes++;
			if (resume === 'never') return new Promise<void>(() => undefined);
			if (resume === 'fail') return Promise.reject(new DOMException('no', 'NotAllowedError'));
			fake.state = 'running';
			return Promise.resolve();
		},
		suspend() {
			fake.suspends++;
			fake.state = 'suspended';
			return Promise.resolve();
		},
		createOscillator() {
			const osc = {
				type: '',
				frequency: { value: 0 },
				onended: null as (() => void) | null,
				connect: (node: unknown) => node,
				start: (at: number) => fake.tones.push({ at, frequency: osc.frequency.value }),
				stop: () => {
					if (endsTone) queueMicrotask(() => osc.onended?.());
				}
			};
			return osc;
		},
		createGain() {
			return {
				gain: { setValueAtTime: () => undefined, exponentialRampToValueAtTime: () => undefined },
				connect: (node: unknown) => node
			};
		}
	};
	return fake;
}

function beeper(fake: ReturnType<typeof fakeAudio> | null) {
	const make = vi.fn(() => fake as unknown as Tone | null);
	return { make, ...createBeeper(make) };
}

afterEach(() => {
	vi.useRealTimers();
});

describe('createBeeper', () => {
	it('plays one short tone and lets audio rest after it', async () => {
		const fake = fakeAudio('running');
		const { beep } = beeper(fake);
		await beep();
		expect(fake.tones).toEqual([{ at: 2, frequency: 1760 }]);
		expect(fake.state).toBe('suspended');
	});

	it('starts suspended audio before the tone', async () => {
		const fake = fakeAudio('suspended');
		const { beep } = beeper(fake);
		await beep();
		expect(fake.resumes).toBe(1);
		expect(fake.tones).toHaveLength(1);
		expect(fake.state).toBe('suspended');
	});

	it('stays silent when audio does not start, and never sounds later', async () => {
		vi.useFakeTimers();
		const fake = fakeAudio('suspended', 'never');
		const { beep } = beeper(fake);
		const playing = beep();
		await vi.advanceTimersByTimeAsync(250);
		await playing;
		fake.state = 'running';
		await vi.advanceTimersByTimeAsync(1000);
		expect(fake.tones).toEqual([]);
	});

	it('stays silent when the browser refuses audio', async () => {
		const fake = fakeAudio('suspended', 'fail');
		const { beep } = beeper(fake);
		await beep();
		expect(fake.tones).toEqual([]);
	});

	it('stops waiting for a tone a suspended context never ends', async () => {
		vi.useFakeTimers();
		const fake = fakeAudio('running', 'ok', false);
		const { beep } = beeper(fake);
		let done = false;
		void beep().then(() => (done = true));
		await vi.advanceTimersByTimeAsync(400);
		expect(done).toBe(true);
		expect(fake.state).toBe('suspended');
	});

	it('unlocks audio on a tap and lets it rest', async () => {
		const fake = fakeAudio('suspended');
		const { prime } = beeper(fake);
		prime();
		await Promise.resolve();
		await Promise.resolve();
		expect(fake.resumes).toBe(1);
		expect(fake.suspends).toBe(1);
		expect(fake.state).toBe('suspended');
		expect(fake.tones).toEqual([]);
	});

	it('makes one context and leaves running audio alone on a tap', async () => {
		const fake = fakeAudio('running');
		const { make, prime, beep } = beeper(fake);
		prime();
		expect(fake.resumes).toBe(0);
		await beep();
		await beep();
		expect(make).toHaveBeenCalledTimes(1);
		expect(fake.tones).toHaveLength(2);
	});

	it('does nothing without Web Audio', async () => {
		const missing = beeper(null);
		missing.prime();
		await missing.beep();
		expect(missing.make).toHaveBeenCalledTimes(1);

		const broken = createBeeper(() => {
			throw new Error('no audio');
		});
		broken.prime();
		await expect(broken.beep()).resolves.toBeUndefined();
	});
});
