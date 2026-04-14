// BUG: Memory leak when the state machine resets
// TODO: Implement TSharedPtr for these handles
void FStateTracker::Reset() {
    // REWORK: This logic is from UE4, update for UE5
    delete InternalState;
}
