/** The part of an AudioContext the beep uses. */
export type Tone = Pick<
	AudioContext,
	'state' | 'currentTime' | 'destination' | 'resume' | 'suspend' | 'createOscillator' | 'createGain'
>;

/** How long a beep waits for audio to start before it gives up, so it never sounds late. */
const startWait = 200;
const length = 0.12;
/** A context suspended mid-beep never ends the tone, so waiting for its end has a cap. */
const endWait = length * 1000 + 250;

/** Whether audio plays now; a function, because the state changes behind the caller's back. */
function running(c: Tone): boolean {
	return c.state === 'running';
}

function after(ms: number): Promise<false> {
	return new Promise((resolve) => setTimeout(() => resolve(false), ms));
}

/** Whether the beep can sound: `ready`, `locked` until a tap or key press, or `none` without Web Audio. */
export type Sound = 'ready' | 'locked' | 'none';

/**
 * A short read beep made in the app with Web Audio, with no file to fetch. iOS starts audio only
 * from a tap or a key press, so `unlock` runs in one; until then a beep stays silent rather than
 * sounding after the next tap. Audio stays on from `unlock` or a beep until `rest`, because
 * starting it again for every read could miss the read; the camera rests it when it stops.
 */
export function createBeeper(make: () => Tone | null) {
	let context: Tone | null | undefined;
	let sounding = 0;
	let unlocked = false;
	let awake = false;

	function audio(): Tone | null {
		if (context === undefined) {
			try {
				context = make();
			} catch {
				context = null;
			}
		}
		return context;
	}

	function sleep(c: Tone) {
		if (!awake && sounding === 0 && running(c)) void c.suspend().catch(() => undefined);
	}

	/**
	 * Starts audio, true when it runs within startWait. It calls resume at once, so it works
	 * inside a tap's handler; audio that starts later counts as unlocked, and sleeps again when
	 * it was rested meanwhile.
	 */
	function started(c: Tone): Promise<boolean> {
		awake = true;
		if (running(c)) {
			unlocked = true;
			return Promise.resolve(true);
		}
		const resumed = c.resume().then(
			() => {
				unlocked = true;
				sleep(c);
				return true;
			},
			() => false
		);
		return Promise.race([resumed, after(startWait)]);
	}

	/** Starts audio and keeps it on; call it from a tap or key press. True once a beep can sound. */
	function unlock(): Promise<boolean> {
		const c = audio();
		return c ? started(c) : Promise.resolve(false);
	}

	/** Suspends audio once no beep is sounding, until the next unlock or beep. */
	function rest() {
		awake = false;
		if (context) sleep(context);
	}

	/** Whether a beep can sound now. */
	function sound(): Sound {
		if (!audio()) return 'none';
		return unlocked ? 'ready' : 'locked';
	}

	/** Plays one short beep; true when it sounded, false when audio cannot start. */
	async function beep(): Promise<boolean> {
		const c = audio();
		if (!c) return false;
		sounding++;
		try {
			if (!(await started(c))) return false;
			const at = c.currentTime;
			const osc = c.createOscillator();
			const gain = c.createGain();
			osc.type = 'sine';
			osc.frequency.value = 1760;
			gain.gain.setValueAtTime(0.0001, at);
			gain.gain.exponentialRampToValueAtTime(0.4, at + 0.008);
			gain.gain.exponentialRampToValueAtTime(0.0001, at + length);
			osc.connect(gain).connect(c.destination);
			const ended = new Promise<true>((resolve) => {
				osc.onended = () => resolve(true);
			});
			osc.start(at);
			osc.stop(at + length + 0.01);
			await Promise.race([ended, after(endWait)]);
			return true;
		} finally {
			sounding--;
			sleep(c);
		}
	}

	return { unlock, rest, sound, beep };
}

const shared = createBeeper(() =>
	typeof AudioContext === 'undefined' ? null : new AudioContext({ latencyHint: 'interactive' })
);

/** Starts the read beep's audio and keeps it on; call it from a tap or key press. */
export const unlockBeep = shared.unlock;

/** Starts the read beep's audio from an event handler that needs no answer. */
export function primeBeep() {
	void shared.unlock();
}

/** Suspends the read beep's audio, as when the camera stops. */
export const restBeep = shared.rest;

/** Whether the read beep can sound now. */
export const beepSound = shared.sound;

/** Plays the read beep; true when it sounded. */
export const beep = shared.beep;
