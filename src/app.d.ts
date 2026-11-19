declare global {
	namespace App {
		interface PageState {
			collectionSort?: import('#lib/collection-sort.js').CollectionSort;
			/** A title page opened from the collection, whose way back is one step back in history. */
			fromCollection?: boolean;
		}
	}
}

export {};
