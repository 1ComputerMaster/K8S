package main

func useMem(buf []byte) {
	return
}

func memAlloc() []byte {
	sliceA := make([]byte, 100)
	useMem(sliceA)

	sliceB := make([]byte, 100)
	useMem(sliceB)

	return sliceB
}

func main() {
	_ = memAlloc()
}
