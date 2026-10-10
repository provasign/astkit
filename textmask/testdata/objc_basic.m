#import <Foundation/Foundation.h>

// comment with 'apostrophe
@interface Greeter : NSObject
- (NSString *)greet;
@end

@implementation Greeter
- (NSString *)greet {
    /* block: - (void)phantom; */
    NSString *s = @"hello \"world\" // not comment";
    char c = '\'';
    return s;
}
@end
