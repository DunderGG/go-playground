/* 
 * HACK: temporary workaround for UE5 physics bug
 * TODO-Dunder: find a better way to handle substepping
 */
class AFakePhysics {
    // BUG: gravity constant is hardcoded
    float Gravity = -980.f;
};
