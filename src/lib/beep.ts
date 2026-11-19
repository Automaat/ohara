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

/**
 * A short read beep made in the app with Web Audio, with no file to fetch. iOS starts audio only
 * from a tap or a key press, so `prime` runs in one; until then a beep stays silent rather than
 * sounding after the next tap. Audio is suspended between beeps, which keeps the device idle.
 */
export function createBeeper(make: () => Tone | null) {
	let context: Tone | null | undefined;
	let sounding = 0;

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

	function rest(c: Tone) {
		if (sounding === 0 && running(c)) void c.suspend().catch(() => undefined);
	}

	async function started(c: Tone): Promise<boolean> {
		if (running(c)) return true;
		const resumed = c.resume().then(
			() => true,
			() => false
		);
		return (await Promise.race([resumed, after(startWait)])) && running(c);
	}

	/** Unlocks audio from a tap or key press; call it from the event handler. */
	function prime() {
		const c = audio();
		if (!c || running(c)) return;
		c.resume().then(
			() => rest(c),
			() => undefined
		);
	}

	/** Plays one short beep, or nothing when audio cannot start. */
	async function beep() {
		const c = audio();
		if (!c) return;
		sounding++;
		try {
			if (!(await started(c))) return;
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
		} finally {
			sounding--;
			rest(c);
		}
	}

	return { prime, beep };
}

const shared = createBeeper(() =>
	typeof AudioContext === 'undefined' ? null : new AudioContext({ latencyHint: 'interactive' })
);

/** Unlocks the read beep from a tap or key press. */
export const primeBeep = shared.prime;

/** Plays the read beep. */
export const beep = shared.beep;
