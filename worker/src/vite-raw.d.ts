// Vite's `?raw` import suffix, which is how the beacon source reaches the Worker
// bundle without a second copy of the file.
declare module "*?raw" {
	const source: string;
	export default source;
}
