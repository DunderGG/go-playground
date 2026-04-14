// TODO: refactor this inefficient loop
void ProcessData() {
    for(int i=0; i<100; i++) {
        // FIXME: potential null pointer dereference
        Data[i]->Update();
    }
}
