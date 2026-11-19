declare global {
	namespace App {
		interface PageState {
			collectionSort?: import('#lib/collection-sort.js').CollectionSort;
			/** The scanner's sheet for a barcode, over the camera at /scan. */
			scanSheet?: { barcode: string; id: number; typed: boolean };
			/** A title page opened from the collection, whose way back is one step back in history. */
			fromCollection?: boolean;
		}
	}
}

export {};
