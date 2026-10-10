#!/usr/bin/env python3
# -*- coding: utf-8 -*-
import logging

log = logging.getLogger(__name__)


class Svc:
    '''Don't do this: self.cache = Redis()'''

    def start(self):
        log.info('starting up')
        self.client = Foo()
        x = 'a'
        y = "it's # not a comment"
        z = r"\""  # raw string with escaped quote
        w = rb'\x00' + Rb"bytes" + BR'more' + u'uni'
        doc = """
        def phantom(self):
            return 'nope'
        """
        msg = f"v={label(3)} {{literal}} {x!r} {y:>10} {z:{width}}"
        nested = f"{d['key']} and {', '.join(items)}"
        multi = f'''
        {compute(1,
                 2)}
        '''
        cont = 'line one \
line two'
        named = f"\N{EM DASH} {value}"
        rawf = rf"\d+{pattern}"
        return self.client  # trailing comment with 'quote
