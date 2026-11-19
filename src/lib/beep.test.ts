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
	it('plays one short tone and keeps audio on until it rests', async () => {
		const fake = fakeAudio('running');
		const { beep, rest } = beeper(fake);
		await expect(beep()).resolves.toBe(true);
		expect(fake.tones).toEqual([{ at: 2, frequency: 1760 }]);
		expect(fake.state).toBe('running');
		rest();
		expect(fake.state).toBe('suspended');
	});

	it('starts suspended audio before the tone', async () => {
		const fake = fakeAudio('suspended');
		const { beep, sound } = beeper(fake);
		await expect(beep()).resolves.toBe(true);
		expect(fake.resumes).toBe(1);
		expect(fake.tones).toHaveLength(1);
		expect(sound()).toBe('ready');
	});

	it('stays silent when audio does not start, and never sounds later', async () => {
		vi.useFakeTimers();
		const fake = fakeAudio('suspended', 'never');
		const { beep, sound } = beeper(fake);
		const playing = beep();
		await vi.advanceTimersByTimeAsync(250);
		await expect(playing).resolves.toBe(false);
		fake.state = 'running';
		await vi.advanceTimersByTimeAsync(1000);
		expect(fake.tones).toEqual([]);
		expect(sound()).toBe('locked');
	});

	it('stays silent when the browser refuses audio', async () => {
		const fake = fakeAudio('suspended', 'fail');
		const { beep, sound } = beeper(fake);
		await expect(beep()).resolves.toBe(false);
		expect(fake.tones).toEqual([]);
		expect(sound()).toBe('locked');
	});

	it('stops waiting for a tone a suspended context never ends', async () => {
		vi.useFakeTimers();
		const fake = fakeAudio('running', 'ok', false);
		const { beep } = beeper(fake);
		let done = false;
		void beep().then(() => (done = true));
		await vi.advanceTimersByTimeAsync(400);
		expect(done).toBe(true);
	});

	it('waits for a sounding beep before it rests', async () => {
		vi.useFakeTimers();
		const fake = fakeAudio('running', 'ok', false);
		const { beep, rest } = beeper(fake);
		const playing = beep();
		await vi.advanceTimersByTimeAsync(0);
		rest();
		expect(fake.state).toBe('running');
		await vi.advanceTimersByTimeAsync(400);
		await playing;
		expect(fake.state).toBe('suspended');
	});

	it('unlocks audio on a tap and keeps it on until it rests', async () => {
		const fake = fakeAudio('suspended');
		const { unlock, rest, sound } = beeper(fake);
		expect(sound()).toBe('locked');
		await expect(unlock()).resolves.toBe(true);
		expect(fake.resumes).toBe(1);
		expect(fake.state).toBe('running');
		expect(fake.tones).toEqual([]);
		expect(sound()).toBe('ready');
		await expect(unlock()).resolves.toBe(true);
		expect(fake.resumes).toBe(1);
		rest();
		expect(fake.state).toBe('suspended');
		await expect(unlock()).resolves.toBe(true);
		expect(fake.resumes).toBe(2);
	});

	it('reports audio that is not allowed to start yet as locked', async () => {
		vi.useFakeTimers();
		const fake = fakeAudio('suspended', 'never');
		const { unlock, sound } = beeper(fake);
		const unlocking = unlock();
		await vi.advanceTimersByTimeAsync(250);
		await expect(unlocking).resolves.toBe(false);
		expect(sound()).toBe('locked');
	});

	it('puts audio that starts after a rest back to sleep without a tone', async () => {
		vi.useFakeTimers();
		let release = () => undefined as void;
		const fake = fakeAudio('suspended', 'never');
		fake.resume = () => {
			fake.resumes++;
			return new Promise<void>((resolve) => {
				release = () => {
					fake.state = 'running';
					resolve();
				};
			});
		};
		const { beep, rest, sound } = beeper(fake);
		const playing = beep();
		await vi.advanceTimersByTimeAsync(250);
		await expect(playing).resolves.toBe(false);
		rest();
		release();
		await vi.advanceTimersByTimeAsync(0);
		expect(fake.tones).toEqual([]);
		expect(fake.state).toBe('suspended');
		expect(sound()).toBe('ready');
	});

	it('makes one context and counts running audio as unlocked', async () => {
		const fake = fakeAudio('running');
		const { make, unlock, beep } = beeper(fake);
		await expect(unlock()).resolves.toBe(true);
		expect(fake.resumes).toBe(0);
		await beep();
		await beep();
		expect(make).toHaveBeenCalledTimes(1);
		expect(fake.tones).toHaveLength(2);
	});

	it('does nothing without Web Audio', async () => {
		const missing = beeper(null);
		expect(missing.sound()).toBe('none');
		await expect(missing.unlock()).resolves.toBe(false);
		await expect(missing.beep()).resolves.toBe(false);
		missing.rest();
		expect(missing.make).toHaveBeenCalledTimes(1);

		const broken = createBeeper(() => {
			throw new Error('no audio');
		});
		await expect(broken.unlock()).resolves.toBe(false);
		await expect(broken.beep()).resolves.toBe(false);
		expect(broken.sound()).toBe('none');
	});
});
