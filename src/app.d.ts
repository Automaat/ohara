declare global {
	namespace App {
		interface PageState {
			collectionSort?: import('$lib/collection-sort').CollectionSort;
		}
	}
}

export {};
